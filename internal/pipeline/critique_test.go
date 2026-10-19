package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/model"
)

// scriptCompleter replies from a script and records the prompts it got.
type scriptCompleter struct {
	replies []string
	prompts []string
	calls   int
}

func (s *scriptCompleter) Name() string { return "script" }
func (s *scriptCompleter) Complete(_ context.Context, _, user string, _ llm.Options) (string, llm.Usage, error) {
	s.prompts = append(s.prompts, user)
	s.calls++
	if s.calls > len(s.replies) {
		return "", llm.Usage{}, nil // script exhausted: an empty answer
	}
	return s.replies[s.calls-1], llm.Usage{InputTokens: 100, OutputTokens: 50}, nil
}

func ep(doi string) model.EnrichedPaper {
	return model.EnrichedPaper{Paper: model.Paper{
		DOI: doi, Title: "Study " + doi, Year: 2023, Abstract: "A randomized trial on " + doi,
		StudyDesign: "rct", Source: "consensus",
	}}
}

func crit(doi, quality, relevance, direction string) model.Critique {
	return model.Critique{DOI: doi, DesignQuality: quality, SampleAdequacy: "adequate",
		Relevance: relevance, ResultDirection: direction, Strengths: []string{"x"}, Concerns: []string{"y"}}
}

// The batch must match critiques to studies by DOI, and studies the batch
// dropped must fall back to single calls.
func TestCritiqueBatchFallsBackForDroppedStudies(t *testing.T) {
	batch := `{"critiques":[` +
		`{"doi":"10.1/a","design_quality":"high","relevance":"direct","result_direction":"positive"},` +
		`{"doi":"10.1/b","design_quality":"moderate","relevance":"indirect","result_direction":"null"}]}` // c dropped
	single := `{"doi":"10.1/c","design_quality":"low","relevance":"tangential","result_direction":"unclear"}`
	s := &scriptCompleter{replies: []string{batch, single}}

	got, _ := critiqueBatch(context.Background(), s, []model.EnrichedPaper{ep("10.1/a"), ep("10.1/b")}, "q")
	if len(got) != 2 {
		t.Fatalf("batch must return its two studies: %v", got)
	}
	fb, _ := critiquePaper(context.Background(), s, ep("10.1/c"), "q")

	if got["10.1/a"].DesignQuality != "high" || got["10.1/a"].Relevance != "direct" {
		t.Errorf("a: %+v", got["10.1/a"])
	}
	if fb.DesignQuality != "low" {
		t.Errorf("fallback single call: %+v", fb)
	}
	if len(s.prompts) != 2 || !strings.Contains(s.prompts[0], `"studies"`) {
		t.Errorf("want one batch prompt then one single prompt, got %d", len(s.prompts))
	}
	if !strings.Contains(s.prompts[1], "Assess this study") {
		t.Errorf("fallback must use the single-study form:\n%s", s.prompts[1])
	}
}

// A total batch failure degrades to single calls for the whole chunk.
func TestCritiqueBatchTotalFailureReturnsNil(t *testing.T) {
	s := &scriptCompleter{replies: []string{"complete garbage, not json at all", "still not json"}}
	got, _ := critiqueBatch(context.Background(), s, []model.EnrichedPaper{ep("10.1/a")}, "q")
	if got != nil {
		t.Fatalf("failed batch must return nil map, got %v", got)
	}
	if s.calls != 2 { // the ask + the (payload-carrying) retry
		t.Fatalf("calls: %d", s.calls)
	}
}

// The stage reuses cached critiques and writes new ones back.
func TestCritiqueStageUsesCache(t *testing.T) {
	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "critique-cache.json")
	seeded := CritiqueCache{
		critiqueCacheKey(ep("10.1/a"), "q?"): {CheckedAt: time.Now(), Critique: crit("10.1/a", "high", "direct", "positive")},
	}
	if err := seeded.Save(cachePath); err != nil {
		t.Fatal(err)
	}

	batch := `{"critiques":[{"doi":"10.1/b","design_quality":"low","relevance":"tangential","result_direction":"unclear"}]}`
	s := &scriptCompleter{replies: []string{batch}}
	p := New(&config.Config{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTLDays: 30},
		Clients{LLM: s}, quietLog())
	st := &State{Question: "q?", Enriched: []model.EnrichedPaper{ep("10.1/a"), ep("10.1/b")}}
	if err := p.critique(context.Background(), st, Options{RunDir: filepath.Join(tmp, "run-1")}); err != nil {
		t.Fatal(err)
	}
	if s.calls != 1 {
		t.Fatalf("cached study must not be re-asked: %d calls", s.calls)
	}
	if st.Critiques[0].DesignQuality != "high" || st.Critiques[1].DesignQuality != "low" {
		t.Fatalf("critiques: %+v", st.Critiques)
	}
	if st.Cost == nil || st.Cost.Critique == nil || st.Cost.Critique.Calls != 1 {
		t.Fatalf("cost must count only real calls: %+v", st.Cost)
	}
	saved := LoadCritiqueCache(cachePath, 30*24*time.Hour)
	if _, ok := saved[critiqueCacheKey(ep("10.1/b"), "q?")]; !ok {
		t.Errorf("fresh critique not written back: %d entries", len(saved))
	}

	// A second critique stage over the same corpus: pure cache, zero calls.
	s2 := &scriptCompleter{}
	p2 := New(&config.Config{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTLDays: 30},
		Clients{LLM: s2}, quietLog())
	st2 := &State{Question: "q?", Enriched: st.Enriched}
	if err := p2.critique(context.Background(), st2, Options{RunDir: filepath.Join(tmp, "run-2")}); err != nil {
		t.Fatal(err)
	}
	if s2.calls != 0 || st2.Critiques[0].DesignQuality != "high" {
		t.Fatalf("second stage must be cache-only: calls=%d critiques=%+v", s2.calls, st2.Critiques)
	}

	// A dropped full text must MISS the cache: the abstract-only critique is
	// stale for a study the critic should now read in full.
	withFT := ep("10.1/a")
	withFT.FullText = "Methods and results of the full paper..."
	s3 := &scriptCompleter{}
	p3 := New(&config.Config{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTLDays: 30},
		Clients{LLM: s3}, quietLog())
	st3 := &State{Question: "q?", Enriched: []model.EnrichedPaper{withFT}}
	if err := p3.critique(context.Background(), st3, Options{RunDir: filepath.Join(tmp, "run-3")}); err != nil {
		t.Fatal(err)
	}
	if s3.calls == 0 {
		t.Fatal("added full text must re-critique, not serve the cached abstract critique")
	}
}

// PDFs dropped into the run directory after enrichment (the addpdf recipe, a
// resume whose stage-2 artifact survived) enter at critique time and
// re-critique: the cache key includes the full text (regression: the pickup
// lived exclusively in stage 2, which resume skips).
func TestCritiquePicksUpDroppedPDF(t *testing.T) {
	restore := pdfTextExtractor
	pdfTextExtractor = func(b []byte, max int) (string, error) {
		return "DROPPED PDF FULL TEXT", nil
	}
	defer func() { pdfTextExtractor = restore }()

	tmp := t.TempDir()
	runDir := filepath.Join(tmp, "run")
	if err := os.MkdirAll(filepath.Join(runDir, "fulltext"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "fulltext", "10.1_a.pdf"),
		[]byte("%PDF-1.4 stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(tmp, "critique-cache.json")
	seeded := CritiqueCache{
		critiqueCacheKey(ep("10.1/a"), "q?"): {CheckedAt: time.Now(), Critique: crit("10.1/a", "high", "direct", "positive")},
	}
	if err := seeded.Save(cachePath); err != nil {
		t.Fatal(err)
	}

	batch := `{"critiques":[{"doi":"10.1/a","design_quality":"high","relevance":"direct","result_direction":"positive"}]}`
	s := &scriptCompleter{replies: []string{batch}}
	p := New(&config.Config{LLMConcurrency: 1, LLMChunk: 5, CritiqueCacheTTLDays: 30},
		Clients{LLM: s}, quietLog())
	p.dir = runDir
	st := &State{Question: "q?", Enriched: []model.EnrichedPaper{ep("10.1/a")}} // no FullText yet
	if err := p.critique(context.Background(), st, Options{RunDir: runDir}); err != nil {
		t.Fatal(err)
	}
	if s.calls != 1 {
		t.Fatalf("the picked-up PDF must re-critique (cache miss): %d calls", s.calls)
	}
	if !strings.Contains(s.prompts[0], "DROPPED PDF FULL TEXT") {
		t.Error("the critique prompt must carry the picked-up full text")
	}
}

// The judge payload carries an abstract only for direct studies (and when
// the critique is missing); the critique alone carries the rest.
func TestSynthesisUserPromptAbstractDiet(t *testing.T) {
	longAbstract := strings.Repeat("word ", 400) // 2000 chars
	st := &State{Question: "q"}
	st.Enriched = []model.EnrichedPaper{
		{Paper: model.Paper{DOI: "10.1/direct", Title: "D", Abstract: longAbstract}},
		{Paper: model.Paper{DOI: "10.1/indirect", Title: "I", Abstract: longAbstract}},
		{Paper: model.Paper{DOI: "10.1/nocrit", Title: "N", Abstract: longAbstract}},
	}
	st.Critiques = []model.Critique{
		crit("10.1/direct", "high", "direct", "positive"),
		crit("10.1/indirect", "low", "indirect", "null"),
	}
	out := synthesisUserPrompt(st)

	var parsed struct {
		Studies []struct {
			DOI      string `json:"doi"`
			Abstract string `json:"abstract"`
		} `json:"studies"`
	}
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Studies) != 3 {
		t.Fatalf("studies: %d", len(parsed.Studies))
	}
	for _, s := range parsed.Studies {
		switch s.DOI {
		case "10.1/direct", "10.1/nocrit":
			if s.Abstract == "" {
				t.Errorf("%s: abstract must ride along (direct / no critique)", s.DOI)
			}
			if len(s.Abstract) > 1300 {
				t.Errorf("%s: abstract must be capped at 1200 chars, got %d", s.DOI, len(s.Abstract))
			}
		case "10.1/indirect":
			if s.Abstract != "" {
				t.Errorf("indirect study: abstract must be dropped, got %d chars", len(s.Abstract))
			}
		}
	}
}
