package pipeline

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// Addresses of the fixture paper: a publisher location (PDF and the DOI as
// the landing page) listed before a repository copy, and the open-access link.
const (
	pubPDF       = "https://publisher.test/a.pdf"
	repoPDF      = "https://repo.test/a.pdf"
	repoPage     = "https://repo.test/a"
	oaPage       = "https://oa.test/a"
	doiPage      = "https://doi.org/10.1000/a"
	publisherURL = "https://www.nejm.org/doi/10.1000/a"
	pmcA         = "PMC111"
	pmcPageA     = "https://pmc.ncbi.nlm.nih.gov/articles/PMC111/"
	pubmedPage   = "https://pubmed.ncbi.nlm.nih.gov/28827081/"
	articleText  = "## Results\nHR 0.8 (95% CI 0.6-1.0)"
)

// locatedPaper is a paper whose first listed location is the publisher's: the
// order the metadata source gives.
func locatedPaper() *model.EnrichedPaper {
	return &model.EnrichedPaper{
		DOI:    listDOIA,
		OALink: oaPage,
		OALocations: []model.Location{
			{PDFURL: pubPDF, LandingURL: doiPage, Host: model.HostPublisher},
			{PDFURL: repoPDF, LandingURL: repoPage, Host: model.HostRepository, OA: true},
		},
	}
}

// pdfsNone serves no PDF anywhere: every address is a landing page.
func pdfsNone() *fakePDFs {
	return &fakePDFs{fetch: func(string) ([]byte, error) { return []byte("<html>landing</html>"), nil }}
}

func TestUpgradeHTTP(t *testing.T) {
	tests := []struct{ in, want string }{
		{"http://repo.test/a.pdf", repoPDF},
		{"HTTP://repo.test/a.pdf", repoPDF},
		{repoPDF, repoPDF},
		{"http", "http"},
		{"", ""},
		{"ftp://repo.test/a", "ftp://repo.test/a"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, upgradeHTTP(tt.in))
		})
	}
}

// The PDF addresses: repository copies first, then the publisher's, then the
// open-access link, each once, every one over https.
func TestPDFCandidatesOrder(t *testing.T) {
	e := locatedPaper()

	assert.Equal(t, []string{repoPDF, pubPDF, oaPage}, pdfCandidates(e))

	e.OALink = pubPDF // the open-access link repeats a location's PDF
	e.OALocations = append(e.OALocations, model.Location{PDFURL: "http://other.test/a.pdf", Host: model.HostUnknown})

	assert.Equal(t, []string{repoPDF, pubPDF, "https://other.test/a.pdf"}, pdfCandidates(e))

	old := &model.EnrichedPaper{OALink: "http://oa.test/a.pdf"} // an artifact without locations

	assert.Equal(t, []string{"https://oa.test/a.pdf"}, pdfCandidates(old))
	assert.Empty(t, pdfCandidates(&model.EnrichedPaper{}))
}

// A PMC article page is not a PDF to download, a PMC PDF is.
func TestPDFCandidatesSkipPMCPages(t *testing.T) {
	e := &model.EnrichedPaper{
		OALink: "https://www.ncbi.nlm.nih.gov/pmc/articles/7745181",
		OALocations: []model.Location{
			{PDFURL: "https://www.ncbi.nlm.nih.gov/pmc/articles/PMC7745181/pdf/x.pdf", Host: model.HostRepository},
		},
	}

	assert.Equal(t, []string{"https://www.ncbi.nlm.nih.gov/pmc/articles/PMC7745181/pdf/x.pdf"}, pdfCandidates(e))
	assert.Equal(t, "PMC7745181", pmcidFromLinks(e))
}

// The reader's addresses: the PMC page, the landing pages (repository first,
// the DOI after them), then the PDF addresses; the old PMC address is dropped.
func TestReaderCandidatesOrder(t *testing.T) {
	e := locatedPaper()
	e.OALocations = append(e.OALocations,
		model.Location{LandingURL: "https://www.ncbi.nlm.nih.gov/pmc/articles/111", Host: model.HostRepository})

	assert.Equal(t,
		[]string{pmcPageA, repoPage, doiPage, repoPDF, pubPDF, oaPage},
		readerCandidates(e, pmcA))
	assert.Equal(t,
		[]string{repoPage, doiPage, repoPDF, pubPDF, oaPage},
		readerCandidates(e, ""), "no PMCID, no PMC page")
}

func TestIsAbstractPage(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{pubmedPage, true},
		{"http://pubmed.ncbi.nlm.nih.gov/28827081", true},
		{"https://PubMed.NCBI.nlm.nih.gov/28827081/", true},
		{"https://www.ncbi.nlm.nih.gov/pubmed/28827081", true},
		{"https://europepmc.org/abstract/MED/28827081", true},
		{"https://europepmc.org/abstract/med/28827081", true},
		{"https://www.europepmc.org/abstract/MED/28827081", true},
		{"https://pmc.ncbi.nlm.nih.gov/articles/PMC5775064/", false},
		{"https://www.ncbi.nlm.nih.gov/pmc/articles/PMC5775064/", false},
		{"https://europepmc.org/article/PMC/PMC5775064", false},
		{"https://repo.test/pubmed.ncbi.nlm.nih.gov/1", false},
		{"https://publisher.test/article/10.1/a", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, isAbstractPage(tt.in))
		})
	}
}

// Pages that carry only an abstract never reach the reader: not as a landing
// page, not as the open-access link, not as a PDF address.
func TestReaderCandidatesSkipAbstractPages(t *testing.T) {
	e := locatedPaper()
	e.OALocations = append(e.OALocations,
		model.Location{LandingURL: pubmedPage, Host: model.HostRepository},
		model.Location{
			LandingURL: "https://europepmc.org/abstract/MED/28827081", PDFURL: "https://europepmc.org/abstract/MED/1",
			Host: model.HostRepository,
		},
	)

	got := readerCandidates(e, pmcA)

	for _, u := range got {
		assert.False(t, isAbstractPage(u), "%s is an abstract page", u)
	}

	assert.Equal(t, []string{pmcPageA, repoPage, doiPage, repoPDF, pubPDF, oaPage}, got,
		"the other addresses are as before")

	e.OALink = pubmedPage

	assert.Equal(t, []string{pmcPageA, repoPage, doiPage, repoPDF, pubPDF}, readerCandidates(e, pmcA),
		"an open-access link to an abstract page is dropped")
}

func TestDOIOfURL(t *testing.T) {
	tests := []struct {
		in      string
		wantDOI string
		wantOK  bool
	}{
		{"https://doi.org/10.1000/A", "10.1000/a", true},
		{"https://dx.doi.org/10.1000/a", "10.1000/a", true},
		{"https://doi.org", "", false},
		{"https://repo.test/doi.org/10.1000/a", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			doi, ok := doiOfURL(tt.in)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantDOI, doi)
		})
	}
}

// A paper Europe PMC knows but does not serve (outside the open-access
// subset) is read from PubMed Central by the PMCID Europe PMC passed on: no
// ID conversion, no PDF.
func TestFetchFullTextPMCUsesTheEuropePMCID(t *testing.T) {
	epmc := &fakeFullTexts{pmcids: map[string]string{listDOIA: pmcA}, reason: pmcA + " is not in the open-access subset"}
	pmc := &fakePMC{texts: map[string]string{pmcA: articleText}}
	pdfs := pdfsNone()
	p := newTest(t, Settings{}, Clients{FullTexts: epmc, PMC: pmc, PDFs: pdfs}, quietLog())
	e := locatedPaper()

	text, source := p.fetchFullText(t.Context(), t.TempDir(), e, true)

	assert.Equal(t, articleText, text)
	assert.Equal(t, fullTextSourcePMC, source)
	assert.Zero(t, pmc.calls.get("PMCID:"+listDOIA), "the PMCID is known: the ID converter is not asked")
	assert.Equal(t, 1, pmc.calls.get("PMC:"+pmcA))
	assert.Equal(t, []string{repoPDF}, pdfs.asked, "only the PDF copy for the chart check, never the text")
	assert.Empty(t, e.Errors)
}

// With no PMCID anywhere, the ID converter supplies it; with none, the step
// is skipped and the walk goes on.
func TestFetchFullTextPMCIDConverter(t *testing.T) {
	tests := []struct {
		name     string
		pmc      *fakePMC
		wantText string
		wantErr  bool
	}{
		{
			name:     "the converter finds it",
			pmc:      &fakePMC{ids: map[string]string{listDOIA: pmcA}, texts: map[string]string{pmcA: articleText}},
			wantText: articleText,
		},
		{name: "PMC does not hold the paper", pmc: &fakePMC{}},
		{name: "the converter fails", pmc: &fakePMC{idErr: errors.New("HTTP 500")}, wantErr: true},
		{
			name: "front matter only: the body is empty",
			pmc:  &fakePMC{ids: map[string]string{listDOIA: pmcA}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTest(t, Settings{}, Clients{FullTexts: &fakeFullTexts{reason: "no PMCID"}, PMC: tt.pmc}, quietLog())
			e := &model.EnrichedPaper{DOI: listDOIA}

			text, source := p.fetchFullText(t.Context(), t.TempDir(), e, true)

			assert.Equal(t, tt.wantText, text)

			if tt.wantText != "" {
				assert.Equal(t, fullTextSourcePMC, source)
			}

			assert.Equal(t, tt.wantErr, len(e.Errors) > 0, "a converter failure is recorded, a missing PMCID is not")
		})
	}
}

// Every candidate PDF address is tried once, in the order of preference and
// over https, until one is a PDF.
func TestFetchFullTextPDFCandidatesInOrder(t *testing.T) {
	pdfs := &fakePDFs{fetch: func(url string) ([]byte, error) {
		if url == pubPDF {
			return []byte("%PDF-1.4 the publisher's"), nil
		}

		return []byte("<html>landing</html>"), nil
	}}
	p := newTest(t, Settings{}, Clients{PDFs: pdfs, PDFText: fakePDFText{text: "from the publisher PDF"}}, quietLog())
	e := locatedPaper()
	e.OALocations[1].PDFURL = "http://repo.test/a.pdf" // plain http: fetched over https

	text, source := p.fetchFullText(t.Context(), t.TempDir(), e, true)

	assert.Equal(t, "from the publisher PDF", text)
	assert.Equal(t, fullTextSourcePDF, source)
	assert.Equal(t, []string{repoPDF, pubPDF}, pdfs.asked, "the repository first; the open-access link is not reached")
	assert.Empty(t, e.Errors, "the text is there: the skipped address is only in the log")
}

// The reader is the last resort: the PMC page, then the landing pages with a
// doi.org address resolved to the publisher's page, then the PDF addresses;
// the source names the host of the page that gave the text.
func TestFetchFullTextReaderOrder(t *testing.T) {
	reader := &fakeReader{pages: map[string]string{publisherURL: articleText}}
	landings := &fakeLandings{pages: map[string]string{listDOIA: "http://www.nejm.org/doi/10.1000/a"}}
	epmc := &fakeFullTexts{pmcids: map[string]string{listDOIA: pmcA}}
	p := newTest(t, Settings{}, Clients{
		FullTexts: epmc, PMC: &fakePMC{}, Landings: landings, Reader: reader, PDFs: pdfsNone(),
	}, quietLog())
	e := locatedPaper()

	text, source := p.fetchFullText(t.Context(), t.TempDir(), e, true)

	assert.Equal(t, articleText, text)
	assert.Equal(t, "jina:www.nejm.org", source)
	assert.Equal(t, []string{pmcPageA, repoPage, publisherURL}, reader.asked)
	assert.Equal(t, 1, landings.calls.total(), "the DOI is resolved once: the location and the DOI itself are one address")
	assert.Empty(t, e.Errors)
}

// Nothing delivers: the paper records one error that names the reader's
// reasons, and the reader is asked about at most maxReaderTries addresses.
func TestFetchFullTextReaderRejectsEverything(t *testing.T) {
	reader := &fakeReader{}
	landings := &fakeLandings{pages: map[string]string{listDOIA: publisherURL}}
	p := newTest(t, Settings{}, Clients{
		Landings: landings, Reader: reader, PDFs: pdfsNone(), PDFText: fakePDFText{text: "x"},
	}, quietLog())
	e := locatedPaper()

	text, source := p.fetchFullText(t.Context(), t.TempDir(), e, true)

	assert.Empty(t, text)
	assert.Empty(t, source)
	assert.Len(t, reader.asked, maxReaderTries)
	require.Len(t, e.Errors, 1)
	assert.Contains(t, e.Errors[0], "jina repo.test: the page is not a full text")
	assert.Contains(t, e.Errors[0], "pdf repo.test: not a PDF")
}

// Without a resolver a doi.org address is skipped (the reader refuses it),
// and the next candidate is tried.
func TestFetchFullTextReaderWithoutResolver(t *testing.T) {
	reader := &fakeReader{pages: map[string]string{repoPDF: articleText}}
	p := newTest(t, Settings{}, Clients{Reader: reader}, quietLog())
	e := locatedPaper()

	text, source := p.fetchFullText(t.Context(), t.TempDir(), e, true)

	assert.Equal(t, articleText, text)
	assert.Equal(t, "jina:repo.test", source)
	assert.Equal(t, []string{repoPage, repoPDF}, reader.asked, "the doi.org address never reaches the reader")
}

// A DOI doi.org does not know, or a failing resolver, costs only that
// address.
func TestFetchFullTextReaderResolverTrouble(t *testing.T) {
	tests := []struct {
		name     string
		landings *fakeLandings
		wantErr  bool
	}{
		{name: "not registered", landings: &fakeLandings{}},
		{name: "the resolver fails", landings: &fakeLandings{err: errors.New("HTTP 500")}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeReader{pages: map[string]string{oaPage: articleText}}
			p := newTest(t, Settings{}, Clients{Landings: tt.landings, Reader: reader}, quietLog())
			e := locatedPaper()
			e.OALocations = e.OALocations[:1] // the publisher location only: its landing page is the DOI

			text, source := p.fetchFullText(t.Context(), t.TempDir(), e, true)

			assert.Equal(t, articleText, text, "the walk goes on to the next address")
			assert.Equal(t, "jina:oa.test", source)
			assert.Empty(t, e.Errors, "the text is there")
		})
	}
}

// One Info line per study with the outcome and the reason each skipped source
// was skipped; the same line says when nothing was obtained.
func TestFetchFullTextLogsOneLinePerStudy(t *testing.T) {
	var buf bytes.Buffer

	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	epmc := &fakeFullTexts{pmcids: map[string]string{listDOIA: pmcA}, reason: pmcA + " is not in the open-access subset"}
	pmc := &fakePMC{texts: map[string]string{pmcA: articleText}}
	p := newTest(t, Settings{}, Clients{FullTexts: epmc, PMC: pmc}, log)

	text, _ := p.fetchFullText(t.Context(), t.TempDir(), locatedPaper(), true)
	require.NotEmpty(t, text)

	line := strings.TrimSpace(buf.String())
	require.Equal(t, 1, strings.Count(line, "\n")+1, "one line: %s", line)
	assert.Contains(t, line, "level=INFO")
	assert.Contains(t, line, `msg="full text obtained"`)
	assert.Contains(t, line, "source=pmc")
	assert.Contains(t, line, "doi=10.1000/a")
	assert.Contains(t, line, "europepmc: PMC111 is not in the open-access subset")

	buf.Reset()

	nothing := newTest(t, Settings{}, Clients{FullTexts: &fakeFullTexts{reason: "DOI not indexed"}, PMC: &fakePMC{}}, log)
	bare := &model.EnrichedPaper{DOI: listDOIA}

	text, _ = nothing.fetchFullText(t.Context(), t.TempDir(), bare, true)
	require.Empty(t, text)

	line = strings.TrimSpace(buf.String())
	assert.Contains(t, line, `msg="full text not obtained"`)
	assert.Contains(t, line, "europepmc: DOI not indexed")
	assert.Contains(t, line, "pmc: no PMCID")
	assert.Contains(t, line, "pdf: not configured")
	assert.Contains(t, line, "jina: not configured")
}

// A canceled run stops the walk and says so.
func TestFetchFullTextInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	reader := &fakeReader{}
	p := newTest(t, Settings{}, Clients{Reader: reader}, quietLog())

	text, _ := p.fetchFullText(ctx, t.TempDir(), locatedPaper(), true)

	assert.Empty(t, text)
	assert.Empty(t, reader.asked)
}

// A PDF in the run directory still wins over every download source.
func TestFetchFullTextUserPDFBeatsTheNewSources(t *testing.T) {
	runDir := t.TempDir()
	reader := &fakeReader{pages: map[string]string{repoPage: articleText}}
	pmc := &fakePMC{ids: map[string]string{listDOIA: pmcA}, texts: map[string]string{pmcA: articleText}}
	p := newTest(t, Settings{}, Clients{PMC: pmc, Reader: reader, PDFText: fakePDFText{text: "the user's"}}, quietLog())

	require.NoError(t, keepPDF(fullTextPath(runDir, listDOIA), []byte("%PDF-1.4 user")))

	text, source := p.fetchFullText(t.Context(), runDir, locatedPaper(), true)

	assert.Equal(t, "the user's", text)
	assert.Equal(t, fullTextSourcePDF, source)
	assert.Zero(t, pmc.calls.total())
	assert.Empty(t, reader.asked)
	assert.FileExists(t, filepath.Join(runDir, "fulltext", "10.1000_a.pdf"))
}

// The report lists the direct studies with no full text, with the best page
// and the file name for a manual download; the lean mode and an artifact
// without a recorded mode list nothing.
func TestFullTextGaps(t *testing.T) {
	st := &State{
		TextMode: TextHybrid,
		Enriched: []model.EnrichedPaper{
			{DOI: "10.1000/Direct", Title: "Direct, no text", OALocations: []model.Location{
				{LandingURL: "http://doi.org/10.1000/direct", Host: model.HostPublisher},
				{LandingURL: "http://repo.test/direct", Host: model.HostRepository},
			}},
			{DOI: "10.1000/has", Title: "Direct with text", FullText: "text"},
			{DOI: "10.1000/indirect", Title: "Indirect, no text"},
			{DOI: "10.1000/old", Title: "Direct, only the old link", OALink: "http://oa.test/old"},
			{DOI: "", Title: "Direct, no DOI"},
		},
		Critiques: []model.Critique{
			{Relevance: relevanceDirect},
			{Relevance: relevanceDirect},
			{Relevance: relevanceIndirect},
			{Relevance: relevanceDirect},
			{Relevance: relevanceDirect},
		},
	}

	assert.Equal(t, []string{
		"Direct, no text — DOI 10.1000/direct — https://repo.test/direct — `10.1000_direct.pdf`",
		"Direct, only the old link — DOI 10.1000/old — https://oa.test/old — `10.1000_old.pdf`",
	}, fullTextGaps(st))

	st.TextMode = TextFull
	assert.Len(t, fullTextGaps(st), 2)

	st.TextMode = TextLean
	assert.Empty(t, fullTextGaps(st))

	st.TextMode = ""
	assert.Empty(t, fullTextGaps(st))
}

func TestWriteFullTextGaps(t *testing.T) {
	var b strings.Builder

	writeFullTextGaps(&b, &State{TextMode: TextHybrid})
	assert.Empty(t, b.String(), "no gaps, no section")

	st := &State{
		TextMode:  TextHybrid,
		Enriched:  []model.EnrichedPaper{{DOI: "10.1000/x", Title: "T"}},
		Critiques: []model.Critique{{Relevance: relevanceDirect}},
	}

	writeFullTextGaps(&b, st)

	got := b.String()
	assert.Contains(t, got, "\n### Full texts not obtained\n\n")
	assert.Contains(t, got, "`fulltext/<file>`")
	assert.Contains(t, got, "\n\n- T — DOI 10.1000/x — https://doi.org/10.1000/x — `10.1000_x.pdf`\n",
		"a blank line before the list")
}

// The locations survive the enrichment cache and the integrity lookup, and a
// cache entry written before they were kept still loads.
func TestLocationsReachThePaperAndTheCache(t *testing.T) {
	locs := []model.Location{{PDFURL: repoPDF, LandingURL: repoPage, Host: model.HostRepository, OA: true}}
	works := &fakeWorks{byDOI: func(string) (*model.Work, error) {
		return &model.Work{OAURL: oaPage, Locations: locs}, nil
	}}
	p := newTest(t, Settings{}, Clients{Works: works}, quietLog())

	e := model.EnrichedPaper{DOI: listDOIA, Source: model.SourceManual}
	p.checkIntegrity(t.Context(), &e)

	assert.Equal(t, locs, e.OALocations)

	var fresh model.EnrichedPaper

	applyCached(&fresh, CacheEntry{OALink: oaPage, OALocations: locs})
	assert.Equal(t, locs, fresh.OALocations)

	var kept model.EnrichedPaper

	kept.OALocations = []model.Location{{PDFURL: pubPDF}}
	applyCached(&kept, CacheEntry{OALocations: locs})
	assert.Equal(t, []model.Location{{PDFURL: pubPDF}}, kept.OALocations, "what the paper already has wins")

	var old model.EnrichedPaper

	applyCached(&old, CacheEntry{OALink: oaPage}) // an entry from before the field
	assert.Empty(t, old.OALocations)
	assert.Equal(t, oaPage, old.OALink)
}
