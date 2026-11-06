package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// pdfServer serves "%PDF" bytes for every URL except the landing page.
func pdfServer() *fakePDFs {
	return &fakePDFs{fetch: func(url string) ([]byte, error) {
		if strings.Contains(url, "landing") {
			return []byte("<html>not a pdf</html>"), nil
		}

		return []byte("%PDF-1.4 fake-but-sniffable"), nil
	}}
}

func TestFetchFullText(t *testing.T) {
	runDir := t.TempDir()
	pdfs := pdfServer()
	p := newTest(t, Settings{Concurrency: 4}, Clients{
		PDFs:    pdfs,
		PDFText: fakePDFText{text: "METHODS We randomized 240 patients. RESULTS No effect."},
	}, quietLog())

	ok := &model.EnrichedPaper{DOI: listDOIA, OALink: oaPDFA}
	txt, src := p.fetchFullText(context.Background(), runDir, ok, true)
	require.NotEmpty(t, txt)
	require.Contains(t, txt, "240 patients")
	assert.Equal(t, fullTextSourcePDF, src)

	require.Empty(t, ok.FullText, "fetchFullText must not mutate FullText itself") // the caller (enrich) assigns the field

	require.FileExists(t, filepath.Join(runDir, "fulltext", "10.1000_a.pdf"), "PDF not saved for the vision stage")

	landing := &model.EnrichedPaper{DOI: listDOIB, OALink: "https://oa.test/landing"}
	landingTxt, landingSrc := p.fetchFullText(context.Background(), runDir, landing, true)
	require.Empty(t, landingTxt, "landing page must yield no text")
	assert.Empty(t, landingSrc, "no text, no source")

	require.NotEmpty(t, landing.Errors, "non-PDF link must be recorded as an error")
	assert.NoFileExists(t, filepath.Join(runDir, "fulltext", "10.1000_b.pdf"))
}

// Without the download permission an OA link is not followed.
func TestFetchFullTextNoDownloadWhenNotAllowed(t *testing.T) {
	pdfs := pdfServer()
	p := newTest(t, Settings{}, Clients{PDFs: pdfs, PDFText: fakePDFText{text: "x"}}, quietLog())

	e := &model.EnrichedPaper{DOI: listDOIA, OALink: oaPDFA}

	txt, _ := p.fetchFullText(context.Background(), t.TempDir(), e, false)
	assert.Empty(t, txt)
	assert.Zero(t, pdfs.calls.total())
	assert.Empty(t, e.Errors)
}

// Download and extraction failures are recorded on the paper, not raised.
func TestFetchFullTextFailuresAreRecorded(t *testing.T) {
	tests := []struct {
		name    string
		pdfs    PDFSource
		pdftext PDFText
		want    string
	}{
		{
			name:    "download fails",
			pdfs:    &fakePDFs{fetch: func(string) ([]byte, error) { return nil, errors.New("HTTP 403") }},
			pdftext: fakePDFText{text: "x"},
			want:    "HTTP 403",
		},
		{
			name:    "extraction fails",
			pdfs:    pdfServer(),
			pdftext: fakePDFText{err: errors.New("corrupt xref")},
			want:    "text extraction failed: corrupt xref",
		},
		{
			name:    "no text in the PDF",
			pdfs:    pdfServer(),
			pdftext: fakePDFText{text: ""},
			want:    "no extractable text",
		},
		{
			name: "no extractor wired",
			pdfs: pdfServer(),
			want: "no PDF text extractor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTest(t, Settings{}, Clients{PDFs: tt.pdfs, PDFText: tt.pdftext}, quietLog())
			e := &model.EnrichedPaper{DOI: listDOIA, OALink: oaPDFA}

			txt, _ := p.fetchFullText(context.Background(), t.TempDir(), e, true)
			assert.Empty(t, txt)
			require.Len(t, e.Errors, 1)
			assert.Contains(t, e.Errors[0], "fulltext: ")
			assert.Contains(t, e.Errors[0], tt.want)
		})
	}
}

// The agent-written chart critique slots into synthesis as data; a broken
// file must never kill the run.
func TestLoadVisionNotes(t *testing.T) {
	dir := t.TempDir()
	st := &State{}
	p := newTest(t, Settings{}, Clients{}, quietLog())
	p.loadVisionNotes(dir, st) // no file at all

	require.Empty(t, st.VisionNotes, "no artifact must yield no notes")

	broken := filepath.Join(dir, "3b-vision.json")
	require.NoError(t, os.WriteFile(broken, []byte("{not json"), 0o600))

	p.loadVisionNotes(dir, st)

	require.Empty(t, st.VisionNotes, "broken artifact must yield no notes")

	good := `{"vision_notes":[{"doi":"10.1/x","findings":["y-axis truncated at 40% (fig 2)"]}]}`
	require.NoError(t, os.WriteFile(broken, []byte(good), 0o600))

	p.loadVisionNotes(dir, st)

	require.Len(t, st.VisionNotes, 1)
	require.Equal(t, "10.1/x", st.VisionNotes[0].DOI)
	require.Equal(t, "y-axis truncated at 40% (fig 2)", st.VisionNotes[0].Findings[0])
}

// A PDF pre-placed by the user wins over download, works without the
// --fulltext flag and without an OA link (the paywall workaround).
func TestFetchFullTextPreplaced(t *testing.T) {
	runDir := t.TempDir()
	pdfs := pdfServer()
	p := newTest(t, Settings{Concurrency: 4}, Clients{
		PDFs:    pdfs,
		PDFText: fakePDFText{text: "USER-SUPPLIED full text of a paywalled paper"},
	}, quietLog())

	require.NoError(t, os.MkdirAll(filepath.Join(runDir, "fulltext"), 0o750))

	pdf := filepath.Join(runDir, "fulltext", "10.1000_paywalled.pdf")
	require.NoError(t, os.WriteFile(pdf, []byte("%PDF-1.4 user-supplied"), 0o600))

	noLink := &model.EnrichedPaper{DOI: "10.1000/paywalled"} // no OALink, no --fulltext
	txt, src := p.fetchFullText(context.Background(), runDir, noLink, false)
	require.Equal(t, "USER-SUPPLIED full text of a paywalled paper", txt, "pre-placed PDF not used")
	assert.Equal(t, fullTextSourcePDF, src)

	require.Empty(t, noLink.Errors, "pre-placed path must not error")
	require.Zero(t, pdfs.calls.total(), "the user's file wins over any download")
}

// PDFDOIs reads the paper's identity out of a user-supplied file.
func TestPDFDOIs(t *testing.T) {
	x := fakePDFText{text: "JAMA Dermatol. doi:10.1001/jamadermatol.2020.0218 Published online."}

	dois, err := PDFDOIs(x, []byte("%PDF-1.4 whatever"))
	require.NoError(t, err)
	require.Equal(t, []string{"10.1001/jamadermatol.2020.0218"}, dois)

	_, err = PDFDOIs(fakePDFText{err: errors.New("corrupt")}, []byte("%PDF"))
	require.ErrorContains(t, err, "corrupt")
}

// Europe PMC's XML is tried before the PDF: no link is needed, and the cap is
// the pipeline's full-text cap. Without an OA link there is no PDF to keep.
func TestFetchFullTextEuropePMCFirst(t *testing.T) {
	runDir := t.TempDir()
	pdfs := pdfServer()
	xml := &fakeFullTexts{texts: map[string]string{listDOIA: "## Results\nHR 0.8 (95% CI 0.6-1.0)"}}
	p := newTest(t, Settings{},
		Clients{FullTexts: xml, PDFs: pdfs, PDFText: fakePDFText{text: "from the PDF"}}, quietLog())

	e := &model.EnrichedPaper{DOI: listDOIA} // no OA link at all
	txt, src := p.fetchFullText(context.Background(), runDir, e, true)

	assert.Equal(t, "## Results\nHR 0.8 (95% CI 0.6-1.0)", txt)
	assert.Equal(t, fullTextSourceEuropePMC, src)
	assert.Equal(t, []int{fullTextMaxChars}, xml.maxes)
	assert.Zero(t, pdfs.calls.total(), "no OA link: nothing to download")
	assert.NoFileExists(t, filepath.Join(runDir, "fulltext", "10.1000_a.pdf"))
	assert.Empty(t, e.Errors)
}

// The XML text stays what the critic reads, and the OA PDF is still saved
// where the vision stage looks for it; its text is never extracted.
func TestFetchFullTextEuropePMCAlsoKeepsPDF(t *testing.T) {
	runDir := t.TempDir()
	pdfs := pdfServer()
	xml := &fakeFullTexts{texts: map[string]string{listDOIA: xmlBody}}
	p := newTest(t, Settings{},
		Clients{FullTexts: xml, PDFs: pdfs, PDFText: fakePDFText{err: errors.New("must not be called")}}, quietLog())

	e := &model.EnrichedPaper{DOI: listDOIA, OALink: oaPDFA}
	txt, src := p.fetchFullText(context.Background(), runDir, e, true)

	assert.Equal(t, xmlBody, txt)
	assert.Equal(t, fullTextSourceEuropePMC, src)
	assert.Equal(t, 1, pdfs.calls.total())
	assert.Equal(t, []byte("%PDF-1.4 fake-but-sniffable"), mustRead(t, filepath.Join(runDir, "fulltext", "10.1000_a.pdf")))
	assert.Empty(t, e.Errors)
}

// A failed PDF download costs nothing when the XML text is there: no paper
// error, no file, the same source.
func TestFetchFullTextEuropePMCPDFFailureIsNotAnError(t *testing.T) {
	tests := []struct {
		name string
		pdfs *fakePDFs
		link string
	}{
		{
			name: "download fails",
			pdfs: &fakePDFs{fetch: func(string) ([]byte, error) { return nil, errors.New("HTTP 403") }},
			link: oaPDFA,
		},
		{name: "landing page", pdfs: pdfServer(), link: "https://oa.test/landing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runDir := t.TempDir()
			xml := &fakeFullTexts{texts: map[string]string{listDOIA: xmlBody}}
			p := newTest(t, Settings{}, Clients{FullTexts: xml, PDFs: tt.pdfs, PDFText: fakePDFText{text: "x"}}, quietLog())
			e := &model.EnrichedPaper{DOI: listDOIA, OALink: tt.link}

			txt, src := p.fetchFullText(context.Background(), runDir, e, true)

			assert.Equal(t, xmlBody, txt)
			assert.Equal(t, fullTextSourceEuropePMC, src)
			assert.Equal(t, 1, tt.pdfs.calls.total())
			assert.Empty(t, e.Errors, "the text is there: a missing PDF is only logged")
			assert.NoFileExists(t, filepath.Join(runDir, "fulltext", "10.1000_a.pdf"))
		})
	}
}

const xmlBody = "xml text"

func mustRead(t *testing.T, path string) []byte {
	t.Helper()

	//nolint:gosec // the path is built from t.TempDir() in the calling test
	b, err := os.ReadFile(path)
	require.NoError(t, err)

	return b
}

// Whatever Europe PMC cannot give — an error, nothing, no client — the OA
// PDF is the fallback; a Europe PMC failure is not recorded as a paper error.
func TestFetchFullTextFallsBackToPDF(t *testing.T) {
	tests := []struct {
		name string
		xml  *fakeFullTexts
	}{
		{name: "europe pmc fails", xml: &fakeFullTexts{err: errors.New("HTTP 500")}},
		{name: "no open-access full text", xml: &fakeFullTexts{}},
		{name: "not wired"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clients := Clients{PDFs: pdfServer(), PDFText: fakePDFText{text: "from the PDF"}}
			if tt.xml != nil {
				clients.FullTexts = tt.xml
			}

			p := newTest(t, Settings{}, clients, quietLog())
			e := &model.EnrichedPaper{DOI: listDOIA, OALink: oaPDFA}

			txt, src := p.fetchFullText(context.Background(), t.TempDir(), e, true)

			assert.Equal(t, "from the PDF", txt)
			assert.Equal(t, fullTextSourcePDF, src)
			assert.Empty(t, e.Errors)
		})
	}
}

// With both sources failing, the PDF failure is what the paper records.
func TestFetchFullTextBothSourcesFail(t *testing.T) {
	xml := &fakeFullTexts{err: errors.New("HTTP 500")}
	pdfs := &fakePDFs{fetch: func(string) ([]byte, error) { return nil, errors.New("HTTP 403") }}
	p := newTest(t, Settings{}, Clients{FullTexts: xml, PDFs: pdfs, PDFText: fakePDFText{text: "x"}}, quietLog())
	e := &model.EnrichedPaper{DOI: listDOIA, OALink: oaPDFA}

	txt, src := p.fetchFullText(context.Background(), t.TempDir(), e, true)

	assert.Empty(t, txt)
	assert.Empty(t, src)
	require.Len(t, e.Errors, 1)
	assert.Contains(t, e.Errors[0], "HTTP 403")
}

// Without the download permission Europe PMC is not asked either, and a PDF
// the user placed wins over the XML even where downloads are allowed.
func TestFetchFullTextEuropePMCRespectsPermissionAndUserFile(t *testing.T) {
	xml := &fakeFullTexts{texts: map[string]string{listDOIA: "xml text"}}
	p := newTest(t, Settings{}, Clients{FullTexts: xml, PDFText: fakePDFText{text: "user PDF text"}}, quietLog())
	e := &model.EnrichedPaper{DOI: listDOIA}

	runDir := t.TempDir()
	txt, _ := p.fetchFullText(context.Background(), runDir, e, false)

	assert.Empty(t, txt)
	assert.Zero(t, xml.calls.total(), "no download permission: Europe PMC is not asked")

	require.NoError(t, os.MkdirAll(filepath.Join(runDir, "fulltext"), 0o750))
	require.NoError(t, os.WriteFile(fullTextPath(runDir, listDOIA), []byte("%PDF-1.4 user"), 0o600))

	txt, src := p.fetchFullText(context.Background(), runDir, e, true)

	assert.Equal(t, "user PDF text", txt)
	assert.Equal(t, fullTextSourcePDF, src)
	assert.Zero(t, xml.calls.total(), "the user's file wins over the XML")
}

// The stored full text is wider than the critic's window, and the window is
// what the critic input and the critique cache key both read: text beyond it
// does not reach the critic and does not invalidate the cache.
func TestCritiqueWindowOverStoredFullText(t *testing.T) {
	require.Greater(t, fullTextMaxChars, critiqueFullTextChars)

	long := strings.Repeat("x", critiqueFullTextChars) + strings.Repeat("y", 100)
	e := model.EnrichedPaper{DOI: listDOIA, FullText: long}

	got, ok := critiqueInput(e)["full_text"].(string)

	require.True(t, ok)
	assert.Equal(t, strings.Repeat("x", critiqueFullTextChars)+"…", got, "the window ends at the cap")

	beyond := e
	beyond.FullText = strings.Repeat("x", critiqueFullTextChars) + strings.Repeat("z", 100)
	inside := e
	inside.FullText = "w" + long[1:]

	assert.Equal(t, critiqueCacheKey(e, "q"), critiqueCacheKey(beyond, "q"), "text beyond the window is not read")
	assert.NotEqual(t, critiqueCacheKey(e, "q"), critiqueCacheKey(inside, "q"), "text inside the window is")
}
