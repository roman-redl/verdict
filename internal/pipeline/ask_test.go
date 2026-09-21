package pipeline

import (
	"strings"
	"testing"

	"github.com/roman-redl/verdict/internal/model"
)

// The newest run carries the full digest; older runs are compact, and papers
// already covered by a newer run are not repeated.
func TestBuildAskSystemCompactOlderRuns(t *testing.T) {
	abstract := "BACKGROUND: a very long abstract that should appear exactly once in the prompt."
	newRun := &State{
		Question: "new question",
		Enriched: []model.EnrichedPaper{
			{Paper: model.Paper{DOI: "10.1/shared", Title: "Shared paper", Abstract: abstract}},
		},
	}
	oldRun := &State{
		Question: "old question",
		Enriched: []model.EnrichedPaper{
			{Paper: model.Paper{DOI: "10.1/shared", Title: "Shared paper", Abstract: abstract}},
			{Paper: model.Paper{DOI: "10.1/old-only", Title: "Old-only paper", Abstract: "old abstract"}},
		},
		Critiques: []model.Critique{
			{DOI: "10.1/shared", DesignQuality: "high", Relevance: "direct", ResultDirection: "positive"},
			{DOI: "10.1/old-only", DesignQuality: "low", Relevance: "indirect", ResultDirection: "null"},
		},
	}

	sys := BuildAskSystem([]RunState{{Name: "new-run", State: newRun}, {Name: "old-run", State: oldRun}})

	if n := strings.Count(sys, abstract); n != 1 {
		t.Errorf("abstract appears %d times, want 1 (newest run only)", n)
	}
	if !strings.Contains(sys, "newest, full context") || !strings.Contains(sys, "older, compact") {
		t.Errorf("run headers must label the digest tier:\n%s", sys)
	}
	if !strings.Contains(sys, "10.1/old-only") {
		t.Errorf("a paper unique to the old run must stay listed")
	}
	old := sys[strings.Index(sys, "older, compact"):]
	if strings.Contains(old, "Old-only paper") && strings.Count(old, "10.1/shared") > 0 {
		// the shared DOI may appear at most as the skip counter, not as a paper line
		if strings.Contains(old, "- 10.1/shared") {
			t.Errorf("paper shared with the newer run must be skipped in the old run's digest:\n%s", old)
		}
	}
	if !strings.Contains(sys, "already listed under a newer run") {
		t.Errorf("skip counter missing:\n%s", sys)
	}
}
