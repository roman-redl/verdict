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

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/model"
)

// The text mode resolves flag → config → hybrid, case-insensitively, and a
// typo is a loud error, never a silent default.
func TestResolveTextMode(t *testing.T) {
	for _, tc := range []struct{ opt, cfg, want string }{
		{"", "", "hybrid"},
		{"", modeLean, modeLean},
		{"full", modeLean, "full"},
		{" HYBRID ", "", "hybrid"},
	} {
		got, err := resolveTextMode(tc.opt, tc.cfg)
		if err != nil || got != tc.want {
			t.Errorf("resolveTextMode(%q,%q) = %q,%v; want %q", tc.opt, tc.cfg, got, err, tc.want)
		}
	}
	if _, err := resolveTextMode("abstracts", ""); err == nil ||
		!strings.Contains(err.Error(), "lean | hybrid | full") {
		t.Errorf("unknown mode must name the valid values, got %v", err)
	}
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
	if len(got) != len(want) {
		t.Fatalf("pivotalStudies: got %v, want %v", got, want)
	}
	for i, idx := range got {
		if st.Enriched[idx].DOI != want[i] {
			t.Fatalf("pivotalStudies: got %v, want %v", got, want)
		}
	}
	if len(pivotalStudies(st, 99)) != 4 { // every direct study, capped only by max
		t.Errorf("uncapped pivotal must keep all 4 direct studies")
	}
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
	if err := os.MkdirAll(runDir, 0o750); err != nil {
		t.Fatal(err)
	}

	a := ep(doiA) // direct, high — pivotal, OA link serves a PDF
	a.OALink = pdfSrv.URL + "/a.pdf"
	b := ep(doiB) // indirect — never fetched even though it has a link
	b.OALink = pdfSrv.URL + "/b.pdf"

	batch := `{"critiques":[` +
		`{"doi":"10.1/a","design_quality":"high","relevance":"direct","result_direction":"positive"},` +
		`{"doi":"10.1/b","design_quality":"moderate","relevance":"indirect","result_direction":"null"}]}`
	synthesis := `{"verdict":"moderate","verdict_rationale":"because ` + strings.Repeat("evidence ", 3) + `10.1/a","consensus_status":"emerging","consensus":"leans positive"}`
	s := &scriptCompleter{replies: []string{batch, synthesis}}

	p := New(&config.Config{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTLDays: 30, HybridPivotalMax: 2},
		Clients{LLM: s}, quietLog())
	p.dir = runDir
	st := &State{
		Question: "q?", Enriched: []model.EnrichedPaper{a, b},
		Critiques: []model.Critique{crit(doiA, qualityHigh, relevanceDirect, directionPositive), crit(doiB, qualityModerate, relevanceIndirect, directionNull)},
		Cost:      &CostReport{},
	}

	if err := p.hybridDeepen(context.Background(), st, Options{RunDir: runDir}); err != nil {
		t.Fatal(err)
	}
	if st.Enriched[0].FullText == "" {
		t.Error("pivotal study must have its full text")
	}
	if st.Enriched[1].FullText != "" {
		t.Error("non-pivotal study must stay on abstracts")
	}
	if !strings.Contains(s.prompts[0], "FULL TEXT METHODS") {
		t.Error("the re-critique prompt must carry the fetched full text")
	}
	if st.Report == nil || st.Report.Verdict != "moderate" {
		t.Fatalf("re-synthesis missing: %+v", st.Report)
	}
	if s.calls != 2 {
		t.Errorf("calls: got %d, want 2 (critique + synthesis)", s.calls)
	}
	if st.Cost.Critique == nil || st.Cost.Critique.Calls != 1 ||
		st.Cost.Synthesis == nil || st.Cost.Synthesis.Calls != 1 {
		t.Errorf("cost must account both stages: %+v", st.Cost)
	}
}

// No OA full text for any pivotal study → the pass is a no-op and the
// abstracts-only verdict stands without extra LLM calls.
func TestHybridDeepenNoFullTextAvailable(t *testing.T) {
	tmp := t.TempDir()
	s := &scriptCompleter{}
	p := New(&config.Config{LLMConcurrency: 1, HybridPivotalMax: 2}, Clients{LLM: s}, quietLog())
	p.dir = tmp
	st := &State{
		Question: "q?", Enriched: []model.EnrichedPaper{ep(doiA)},
		Critiques: []model.Critique{crit(doiA, qualityHigh, relevanceDirect, directionPositive)}, Cost: &CostReport{},
	}
	if err := p.hybridDeepen(context.Background(), st, Options{RunDir: tmp}); err != nil {
		t.Fatal(err)
	}
	if s.calls != 0 || st.Report != nil {
		t.Errorf("no-OA pivotal must skip the pass: calls=%d report=%v", s.calls, st.Report)
	}
}

// The verdictd submit body carries text_mode; the pipeline Options map it
// through (the JSON key is part of the daemon's contract).
func TestHybridDoneSerializes(t *testing.T) {
	st := &State{HybridDone: true}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"hybrid_done":true`) {
		t.Errorf("hybrid_done not serialized: %s", b)
	}
}
