package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// doiC is a third placeholder DOI next to doiA and doiB.
const doiC = "10.1/c"

// The text mode resolves flag → config → hybrid, case-insensitively, and a
// typo is a loud error, never a silent default.
func TestResolveTextMode(t *testing.T) {
	for _, tc := range []struct{ opt, cfg, want TextMode }{
		{"", "", TextHybrid},
		{"", TextLean, TextLean},
		{TextFull, TextLean, TextFull},
		{" HYBRID ", "", TextHybrid},
	} {
		got, err := resolveTextMode(tc.opt, tc.cfg)
		require.NoError(t, err, "resolveTextMode(%q,%q)", tc.opt, tc.cfg)
		assert.Equal(t, tc.want, got, "resolveTextMode(%q,%q)", tc.opt, tc.cfg)
	}

	_, err := resolveTextMode("abstracts", "")
	require.ErrorContains(t, err, "lean | hybrid | full", "unknown mode must name the valid values")
}

func TestParseTextModeAndEngine(t *testing.T) {
	m, err := ParseTextMode(" Lean ")
	require.NoError(t, err)
	assert.Equal(t, TextLean, m)

	m, err = ParseTextMode("")
	require.NoError(t, err, "empty means not set")
	assert.Empty(t, m)

	_, err = ParseTextMode("deep")
	require.ErrorContains(t, err, "lean | hybrid | full")

	e, err := ParseEngine("OpenAlex")
	require.NoError(t, err)
	assert.Equal(t, EngineOpenAlex, e)

	_, err = ParseEngine("bing")
	require.ErrorContains(t, err, "consensus | openalex")

	_, err = ParseEngine("dois")
	require.Error(t, err, "dois is recorded for DOI-list runs, never selected by name")

	var tm TextMode
	require.NoError(t, tm.UnmarshalText([]byte("full")), "the environment parser path")
	assert.Equal(t, TextFull, tm)
	require.Error(t, tm.UnmarshalText([]byte("nope")))
}

// Pivotal = direct studies first, then design quality, then sample size —
// capped; the one indirect study only fills a slot the direct ones leave.
func TestPivotalStudies(t *testing.T) {
	st := &State{}

	for _, spec := range []struct {
		doi, quality, relevance string
		size                    int
	}{
		{doiA, qualityHigh, relevanceDirect, 300},
		{doiB, qualityModerate, relevanceDirect, 5000}, // huge, but a weaker design than a and d
		{doiC, qualityHigh, relevanceIndirect, 9999},   // large, but indirect: after every direct study
		{"10.1/d", qualityHigh, relevanceDirect, 1000}, // outranks a on sample size
		{"10.1/e", qualityLow, relevanceDirect, 0},
	} {
		e := ep(spec.doi)
		e.SampleSize = spec.size

		st.Enriched = append(st.Enriched, e)
		st.Critiques = append(st.Critiques, crit(spec.doi, spec.quality, spec.relevance, directionPositive))
	}

	got := pivotalStudies(st, 3)
	want := []string{"10.1/d", doiA, doiB}

	require.Len(t, got, len(want))

	for i, idx := range got {
		require.Equal(t, want[i], st.Enriched[idx].DOI)
	}

	// every direct study, then the indirect one in the free slot
	all := pivotalStudies(st, 99)
	require.Len(t, all, 5, "uncapped pivotal must keep all 4 direct studies and the indirect one")
	assert.Equal(t, doiC, st.Enriched[all[4]].DOI, "the indirect study comes last")
}

// The deepening pass fetches full texts for the pivotal studies only,
// re-critiques the changed corpus and accumulates the critique cost. It never
// synthesizes: the synthesis stage runs once, after it.
func TestHybridDeepen(t *testing.T) {
	pdfs := &fakePDFs{fetch: func(string) ([]byte, error) { return []byte("%PDF-1.4 fake"), nil }}

	tmp := t.TempDir()

	runDir := filepath.Join(tmp, "run")
	require.NoError(t, os.MkdirAll(runDir, 0o750))

	a := ep(doiA) // direct, high — pivotal, OA link serves a PDF
	a.OALink = oaPDFA
	b := ep(doiB) // tangential — never fetched even though it has a link
	b.OALink = "https://oa.test/b.pdf"

	batch := `{"critiques":[` +
		`{"doi":"10.1/a","design_quality":"high","relevance":"direct","result_direction":"positive"},` +
		`{"doi":"10.1/b","design_quality":"moderate","relevance":"tangential","result_direction":"null"}]}`
	s := &scriptCompleter{replies: []string{batch}}

	p := newTest(t, Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d, HybridPivotalMax: 2},
		Clients{LLM: s, PDFs: pdfs, PDFText: fakePDFText{text: "FULL TEXT METHODS AND RESULTS"}}, quietLog())
	st := &State{
		Question: "q?", Enriched: []model.EnrichedPaper{a, b},
		Critiques: []model.Critique{
			crit(doiA, qualityHigh, relevanceDirect, directionPositive),
			crit(doiB, qualityModerate, relevanceTangent, directionNull),
		},
		Cost: &CostReport{},
	}

	require.NoError(t, p.hybridDeepen(context.Background(), params(Options{RunDir: runDir}), st))

	assert.NotEmpty(t, st.Enriched[0].FullText, "pivotal study must have its full text")

	assert.Empty(t, st.Enriched[1].FullText, "non-pivotal study must stay on abstracts")
	assert.Equal(t, 1, pdfs.calls.total(), "only the pivotal study's PDF is downloaded")

	assert.Contains(t, s.prompts[0], "FULL TEXT METHODS", "the re-critique prompt must carry the fetched full text")

	assert.Nil(t, st.Report, "the deepening must not synthesize")
	assert.Equal(t, 1, s.calls, "want 1 LLM call (the re-critique)")

	require.NotNil(t, st.Cost.Critique, "the re-critique must be accounted")
	assert.Equal(t, 1, st.Cost.Critique.Calls)
	assert.Nil(t, st.Cost.Synthesis, "no synthesis cost: the synthesis stage has not run")
}

// The full-text re-critique goes to the deep client, not to the critic that
// did the abstracts pass; the judge is not asked at all.
func TestHybridDeepenUsesTheDeepClient(t *testing.T) {
	pdfs := &fakePDFs{fetch: func(string) ([]byte, error) { return []byte("%PDF-1.4 fake"), nil }}

	a := ep(doiA)
	a.OALink = oaPDFA

	deepReply := `{"critiques":[{"doi":"10.1/a","design_quality":"high",` +
		`"relevance":"direct","result_direction":"positive"}]}`
	critic, deep, judge := &scriptCompleter{}, &scriptCompleter{replies: []string{deepReply}}, &scriptCompleter{}

	p := newTest(t, Settings{LLMConcurrency: 1, LLMChunk: 5, HybridPivotalMax: 2},
		Clients{LLM: critic, LLMDeep: deep, LLMSynthesis: judge, PDFs: pdfs, PDFText: fakePDFText{text: "FULL TEXT"}},
		quietLog())
	st := &State{
		Question: "q?", Enriched: []model.EnrichedPaper{a},
		Critiques: []model.Critique{crit(doiA, qualityHigh, relevanceDirect, directionPositive)}, Cost: &CostReport{},
	}

	require.NoError(t, p.hybridDeepen(context.Background(), params(Options{RunDir: t.TempDir()}), st))

	assert.Equal(t, 0, critic.calls, "the abstracts critic is not asked again")
	assert.Equal(t, 1, deep.calls, "the deep client re-critiques the pivotal study")
	assert.Equal(t, 0, judge.calls, "the judge is not asked during the deepening")
}

// No OA full text for any pivotal study → the pass is a no-op and the
// abstracts critiques stand without extra LLM calls.
func TestHybridDeepenNoFullTextAvailable(t *testing.T) {
	tmp := t.TempDir()
	s := &scriptCompleter{}
	p := newTest(t, Settings{LLMConcurrency: 1, HybridPivotalMax: 2}, Clients{LLM: s}, quietLog())
	st := &State{
		Question: "q?", Enriched: []model.EnrichedPaper{ep(doiA)},
		Critiques: []model.Critique{crit(doiA, qualityHigh, relevanceDirect, directionPositive)}, Cost: &CostReport{},
	}

	require.NoError(t, p.hybridPass(context.Background(), params(Options{RunDir: tmp}), st))

	assert.Equal(t, 0, s.calls, "no-OA pivotal must skip the pass")
	assert.Nil(t, st.Report, "no-OA pivotal must skip the pass")
	assert.True(t, st.HybridDone, "the pass ran: a resume must not repeat it")
}

// The verdictd submit body carries text_mode; the pipeline Options map it
// through (the JSON key is part of the daemon's contract).
func TestHybridDoneSerializes(t *testing.T) {
	st := &State{HybridDone: true}

	b, err := json.Marshal(st)
	require.NoError(t, err)

	assert.Contains(t, string(b), `"hybrid_done":true`, "hybrid_done not serialized")
}

// Indirect studies take the slots direct ones leave free — same ranking
// inside the tier — while tangential and unknown studies never get one.
func TestPivotalStudiesFillsFreeSlotsWithIndirect(t *testing.T) {
	const (
		indLow  = "10.1/ind-low"
		indHigh = "10.1/ind-high"
		indBig  = "10.1/ind-big"
		dir     = "10.1/dir"
	)

	st := &State{}

	for _, spec := range []struct {
		doi, quality, relevance string
		size                    int
	}{
		{indLow, qualityLow, relevanceIndirect, 5000},
		{indHigh, qualityHigh, relevanceIndirect, 40},
		{"10.1/tan", qualityHigh, relevanceTangent, 9999},
		{dir, qualityLow, relevanceDirect, 0}, // weakest design, still ahead of every indirect
		{"10.1/unk", qualityHigh, valueUnknown, 9999},
		{indBig, qualityHigh, relevanceIndirect, 900},
	} {
		e := ep(spec.doi)
		e.SampleSize = spec.size

		st.Enriched = append(st.Enriched, e)
		st.Critiques = append(st.Critiques, crit(spec.doi, spec.quality, spec.relevance, directionNull))
	}

	dois := func(limit int) []string {
		idxs := pivotalStudies(st, limit)
		out := make([]string, 0, len(idxs))

		for _, idx := range idxs {
			out = append(out, st.Enriched[idx].DOI)
		}

		return out
	}

	assert.Equal(t, []string{dir}, dois(1), "direct first; the cap leaves no slot for indirect")
	assert.Equal(t, []string{dir, indBig, indHigh}, dois(3),
		"free slots go to indirect studies: design quality, then sample size")
	assert.Equal(t, []string{dir, indBig, indHigh, indLow}, dois(99),
		"tangential and unknown never qualify, whatever the cap")
}

// Within a design-quality tier the larger study comes first. The size is the
// enriched paper's, else the critique's reported one; an unknown size is 0 and
// sorts last in its tier; equal sizes keep the corpus order (stable sort);
// citations play no part.
func TestPivotalStudiesOrdersBySampleSize(t *testing.T) {
	type spec struct {
		doi, quality, relevance string
		paperSize, reportedSize int
		cites                   int
	}

	st := &State{}

	for _, s := range []spec{
		{doi: "10.1/no-size", quality: qualityHigh, relevance: relevanceDirect},
		{doi: "10.1/reported-200", quality: qualityHigh, relevance: relevanceDirect, reportedSize: 200},
		// the critic's randomized count wins over the metadata size
		{doi: "10.1/reported-500", quality: qualityHigh, relevance: relevanceDirect, paperSize: 9, reportedSize: 500},
		{doi: "10.1/tie-1", quality: qualityHigh, relevance: relevanceDirect, paperSize: 200},
		{doi: "10.1/tie-2", quality: qualityHigh, relevance: relevanceDirect, paperSize: 200},
		{doi: "10.1/popular-50", quality: qualityHigh, relevance: relevanceDirect, paperSize: 50, cites: 10000},
		{doi: "10.1/mod-big", quality: qualityModerate, relevance: relevanceDirect, paperSize: 100000},
		{doi: "10.1/ind-big", quality: qualityHigh, relevance: relevanceIndirect, paperSize: 100000},
	} {
		e := ep(s.doi)
		e.SampleSize = s.paperSize

		if s.cites > 0 {
			e.Tally = &model.CitationTally{Supporting: s.cites}
		}

		c := crit(s.doi, s.quality, s.relevance, directionNull)
		c.SampleSizeReported = s.reportedSize

		st.Enriched = append(st.Enriched, e)
		st.Critiques = append(st.Critiques, c)
	}

	idxs := pivotalStudies(st, 99)
	got := make([]string, 0, len(idxs))

	for _, idx := range idxs {
		got = append(got, st.Enriched[idx].DOI)
	}

	assert.Equal(t, []string{
		"10.1/reported-500", "10.1/reported-200", "10.1/tie-1", "10.1/tie-2", "10.1/popular-50", "10.1/no-size",
		"10.1/mod-big", "10.1/ind-big",
	}, got)
}

// fetchTestState builds a corpus of direct, high-quality studies, ranked by
// size in the order given: the first is the largest, so it is the first
// candidate of the pivotal walk.
func fetchTestState(dois ...string) *State {
	st := &State{}

	for i, doi := range dois {
		e := ep(doi)
		e.SampleSize = 1000 - i

		st.Enriched = append(st.Enriched, e)
		st.Critiques = append(st.Critiques, crit(doi, qualityHigh, relevanceDirect, directionPositive))
	}

	return st
}

// A pivotal slot whose full text cannot be obtained passes to the next
// candidate, the cap counts obtained texts and the attempts are bounded.
func TestFetchPivotalTexts(t *testing.T) {
	const (
		paywalled = "paywalled"
		open      = "open"
		placed    = "placed"
	)

	// kinds lists the studies in pivotal order: a paywalled one has no text
	// anywhere, an open one is served by Europe PMC, a placed one already has
	// its text (a user PDF or an earlier pass).
	for _, tc := range []struct {
		name         string
		kinds        []string
		maxStudies   int
		wantTried    int
		wantObtained int
		wantFetched  int
	}{
		{
			name:  "paywalled first candidates are skipped",
			kinds: []string{paywalled, paywalled, open, open, open}, maxStudies: 2,
			wantTried: 4, wantObtained: 2, wantFetched: 2,
		},
		{
			name:  "the cap of obtained texts stops the walk",
			kinds: []string{open, open, open, open}, maxStudies: 2,
			wantTried: 2, wantObtained: 2, wantFetched: 2,
		},
		{
			name:  "the attempt bound stops a run of paywalled papers",
			kinds: []string{paywalled, paywalled, paywalled, paywalled, paywalled, paywalled, open}, maxStudies: 2,
			wantTried: 6, wantObtained: 0, wantFetched: 0, // 3 attempts per slot x 2 slots
		},
		{
			name:  "the bound leaves room for what is found before it",
			kinds: []string{paywalled, open, paywalled, paywalled, paywalled, paywalled, paywalled, open}, maxStudies: 2,
			wantTried: 6, wantObtained: 1, wantFetched: 1,
		},
		{
			name:  "pre-existing texts count without a fetch",
			kinds: []string{placed, paywalled, open, placed, open}, maxStudies: 3,
			wantTried: 2, wantObtained: 3, wantFetched: 1,
		},
		{
			name:  "fewer candidates than the cap",
			kinds: []string{paywalled, open}, maxStudies: 5,
			wantTried: 2, wantObtained: 1, wantFetched: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xml := &fakeFullTexts{texts: map[string]string{}}

			var dois []string

			for i, kind := range tc.kinds {
				doi := fmt.Sprintf("10.1/%s-%d", kind, i)
				dois = append(dois, doi)

				if kind == open {
					xml.texts[doi] = "FULL TEXT " + doi
				}
			}

			st := fetchTestState(dois...)

			for i, kind := range tc.kinds {
				if kind == placed {
					st.Enriched[i].FullText = "PLACED TEXT"
				}
			}

			p := newTest(t, Settings{}, Clients{FullTexts: xml}, quietLog())

			got, err := p.fetchPivotalTexts(
				context.Background(), params(Options{RunDir: t.TempDir()}), st, tc.maxStudies)

			require.NoError(t, err)
			assert.Equal(t, pivotalFetch{tried: tc.wantTried, obtained: tc.wantObtained, fetched: tc.wantFetched}, got)
			assert.Equal(t, tc.wantTried, xml.calls.total(), "one Europe PMC lookup per attempt, none for a placed text")
		})
	}
}

// A cancelled run stops the walk with the context's error instead of burning
// the attempts on sources that skip on a dead context.
func TestFetchPivotalTextsStopsOnCancel(t *testing.T) {
	xml := &fakeFullTexts{texts: map[string]string{}}
	st := fetchTestState(doiA, doiB)
	p := newTest(t, Settings{}, Clients{FullTexts: xml}, quietLog())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.fetchPivotalTexts(ctx, params(Options{RunDir: t.TempDir()}), st, 2)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, xml.calls.total())
}

// The deepening re-critiques the studies the slot fallback reached: with the
// first candidate paywalled, the cap of 2 is still filled by the next two.
func TestHybridDeepenPassesSlotsOnToTheNextCandidate(t *testing.T) {
	xml := &fakeFullTexts{texts: map[string]string{doiB: "TEXT B", doiC: "TEXT C"}}
	reply := `{"critiques":[` +
		`{"doi":"10.1/a","design_quality":"high","relevance":"direct","result_direction":"positive"},` +
		`{"doi":"10.1/b","design_quality":"high","relevance":"direct","result_direction":"positive"},` +
		`{"doi":"10.1/c","design_quality":"high","relevance":"direct","result_direction":"positive"},` +
		`{"doi":"10.1/d","design_quality":"high","relevance":"direct","result_direction":"positive"}]}`
	s := &scriptCompleter{replies: []string{reply}}
	p := newTest(t, Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d, HybridPivotalMax: 2},
		Clients{LLM: s, FullTexts: xml}, quietLog())
	st := fetchTestState(doiA, doiB, doiC, "10.1/d") // a is the largest and has no text
	st.Question, st.Cost = "q?", &CostReport{}

	require.NoError(t, p.hybridDeepen(context.Background(), params(Options{RunDir: t.TempDir()}), st))

	assert.Empty(t, st.Enriched[0].FullText)
	assert.Equal(t, "TEXT B", st.Enriched[1].FullText)
	assert.Equal(t, "TEXT C", st.Enriched[2].FullText)
	assert.Empty(t, st.Enriched[3].FullText, "the cap of obtained texts is respected")
	assert.Equal(t, 1, s.calls, "one deep re-critique call")
	assert.Contains(t, s.prompts[0], "TEXT B")
	assert.Contains(t, s.prompts[0], "TEXT C")
}

// An indirect study with a full text is deepened, and the re-critique
// replaces its abstract critique: relevance and direction come from the text.
func TestHybridDeepenIndirectStudyBecomesDirect(t *testing.T) {
	xml := &fakeFullTexts{texts: map[string]string{doiA: "## Results\nSecondary outcome: bleeding 2% vs 5%"}}
	reply := `{"critiques":[{"doi":"10.1/a","design_quality":"high","relevance":"direct","result_direction":"positive"}]}`
	s := &scriptCompleter{replies: []string{reply}}
	p := newTest(t, Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d, HybridPivotalMax: 2},
		Clients{LLM: s, FullTexts: xml}, quietLog())
	st := &State{
		Question: "q?", Enriched: []model.EnrichedPaper{ep(doiA)},
		Critiques: []model.Critique{crit(doiA, qualityHigh, relevanceIndirect, "unclear")}, Cost: &CostReport{},
	}

	require.NoError(t, p.hybridDeepen(context.Background(), params(Options{RunDir: t.TempDir()}), st))

	assert.Equal(t, fullTextSourceEuropePMC, st.Enriched[0].FullTextSource)
	assert.Contains(t, s.prompts[0], "bleeding 2% vs 5%", "the critic reads the XML text")
	assert.Equal(t, relevanceDirect, st.Critiques[0].Relevance, "the deep critique replaces the abstract one")
	assert.Equal(t, directionPositive, st.Critiques[0].ResultDirection)
}
