package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

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

// Pivotal = direct studies first, then design quality, then citation heat —
// capped, non-direct never selected.
func TestPivotalStudies(t *testing.T) {
	st := &State{}

	for _, spec := range []struct {
		doi, quality, relevance string
		heat                    int
	}{
		{doiA, qualityHigh, relevanceDirect, 3},
		{doiB, qualityModerate, relevanceDirect, 0},
		{"10.1/c", qualityHigh, relevanceIndirect, 99}, // hot but not direct
		{"10.1/d", qualityHigh, relevanceDirect, 10},   // outranks a on heat
		{"10.1/e", qualityLow, relevanceDirect, 0},
	} {
		e := ep(spec.doi)
		if spec.heat > 0 {
			e.Tally = &model.CitationTally{Supporting: spec.heat}
		}

		st.Enriched = append(st.Enriched, e)
		st.Critiques = append(st.Critiques, crit(spec.doi, spec.quality, spec.relevance, directionPositive))
	}

	got := pivotalStudies(st, 3)
	want := []string{"10.1/d", doiA, doiB}

	require.Len(t, got, len(want))

	for i, idx := range got {
		require.Equal(t, want[i], st.Enriched[idx].DOI)
	}

	// every direct study, capped only by max
	assert.Len(t, pivotalStudies(st, 99), 4, "uncapped pivotal must keep all 4 direct studies")
}

// The deepening pass fetches full texts for the pivotal studies only,
// re-critiques the changed corpus, re-synthesizes, and accumulates the cost.
func TestHybridDeepen(t *testing.T) {
	restore := pdfTextExtractor
	pdfTextExtractor = func(b []byte, maxChars int) (string, error) {
		return "FULL TEXT METHODS AND RESULTS", nil
	}

	defer func() { pdfTextExtractor = restore }()

	pdfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("%PDF-1.4 fake"))
	}))
	defer pdfSrv.Close()

	tmp := t.TempDir()

	runDir := filepath.Join(tmp, "run")
	require.NoError(t, os.MkdirAll(runDir, 0o750))

	a := ep(doiA) // direct, high — pivotal, OA link serves a PDF
	a.OALink = pdfSrv.URL + "/a.pdf"
	b := ep(doiB) // indirect — never fetched even though it has a link
	b.OALink = pdfSrv.URL + "/b.pdf"

	batch := `{"critiques":[` +
		`{"doi":"10.1/a","design_quality":"high","relevance":"direct","result_direction":"positive"},` +
		`{"doi":"10.1/b","design_quality":"moderate","relevance":"indirect","result_direction":"null"}]}`
	synthesis := `{"verdict":"moderate","verdict_rationale":"because ` + strings.Repeat("evidence ", 3) +
		`10.1/a","consensus_status":"emerging","consensus":"leans positive"}`
	s := &scriptCompleter{replies: []string{batch, synthesis}}

	p := New(Settings{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTL: ttl30d, HybridPivotalMax: 2},
		Clients{LLM: s}, quietLog())
	p.dir = runDir
	st := &State{
		Question: "q?", Enriched: []model.EnrichedPaper{a, b},
		Critiques: []model.Critique{
			crit(doiA, qualityHigh, relevanceDirect, directionPositive),
			crit(doiB, qualityModerate, relevanceIndirect, directionNull),
		},
		Cost: &CostReport{},
	}

	require.NoError(t, p.hybridDeepen(context.Background(), st, Options{RunDir: runDir}))

	assert.NotEmpty(t, st.Enriched[0].FullText, "pivotal study must have its full text")

	assert.Empty(t, st.Enriched[1].FullText, "non-pivotal study must stay on abstracts")

	assert.Contains(t, s.prompts[0], "FULL TEXT METHODS", "the re-critique prompt must carry the fetched full text")

	require.NotNil(t, st.Report, "re-synthesis missing")
	assert.Equal(t, "moderate", st.Report.Verdict, "re-synthesis missing")

	assert.Equal(t, 2, s.calls, "want 2 LLM calls (critique + synthesis)")

	require.NotNil(t, st.Cost.Critique, "cost must account both stages")
	require.NotNil(t, st.Cost.Synthesis, "cost must account both stages")
	assert.Equal(t, 1, st.Cost.Critique.Calls, "cost must account both stages")
	assert.Equal(t, 1, st.Cost.Synthesis.Calls, "cost must account both stages")
}

// No OA full text for any pivotal study → the pass is a no-op and the
// abstracts-only verdict stands without extra LLM calls.
func TestHybridDeepenNoFullTextAvailable(t *testing.T) {
	tmp := t.TempDir()
	s := &scriptCompleter{}
	p := New(Settings{LLMConcurrency: 1, HybridPivotalMax: 2}, Clients{LLM: s}, quietLog())
	p.dir = tmp
	st := &State{
		Question: "q?", Enriched: []model.EnrichedPaper{ep(doiA)},
		Critiques: []model.Critique{crit(doiA, qualityHigh, relevanceDirect, directionPositive)}, Cost: &CostReport{},
	}

	require.NoError(t, p.hybridDeepen(context.Background(), st, Options{RunDir: tmp}))

	assert.Equal(t, 0, s.calls, "no-OA pivotal must skip the pass")
	assert.Nil(t, st.Report, "no-OA pivotal must skip the pass")
}

// The verdictd submit body carries text_mode; the pipeline Options map it
// through (the JSON key is part of the daemon's contract).
func TestHybridDoneSerializes(t *testing.T) {
	st := &State{HybridDone: true}

	b, err := json.Marshal(st)
	require.NoError(t, err)

	assert.Contains(t, string(b), `"hybrid_done":true`, "hybrid_done not serialized")
}
