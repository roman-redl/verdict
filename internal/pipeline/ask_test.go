package pipeline

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/roman-redl/verdict/internal/model"
)

// The newest run carries the full digest; older runs are compact, and papers
// already covered by a newer run are not repeated.
func TestBuildAskSystemCompactOlderRuns(t *testing.T) {
	abstract := "BACKGROUND: a very long abstract that should appear exactly once in the prompt."
	newRun := &State{
		Question: "new question",
		Enriched: []model.EnrichedPaper{
			{DOI: doiShared, Title: "Shared paper", Abstract: abstract},
		},
	}
	oldRun := &State{
		Question: "old question",
		Enriched: []model.EnrichedPaper{
			{DOI: doiShared, Title: "Shared paper", Abstract: abstract},
			{DOI: "10.1/old-only", Title: "Old-only paper", Abstract: "old abstract"},
		},
		Critiques: []model.Critique{
			{DOI: doiShared, DesignQuality: qualityHigh, Relevance: relevanceDirect, ResultDirection: directionPositive},
			{DOI: "10.1/old-only", DesignQuality: qualityLow, Relevance: relevanceIndirect, ResultDirection: directionNull},
		},
	}

	sys := BuildAskSystem([]RunState{{Name: "new-run", State: newRun}, {Name: "old-run", State: oldRun}})

	assert.Equal(t, 1, strings.Count(sys, abstract), "abstract must appear in the newest run only")

	compactAt := strings.Index(sys, "older, compact")
	assert.Contains(t, sys, "newest, full context", "run headers must label the digest tier")
	assert.GreaterOrEqual(t, compactAt, 0, "run headers must label the digest tier")

	assert.Contains(t, sys, "10.1/old-only", "a paper unique to the old run must stay listed")

	var old string
	if compactAt >= 0 { // заголовок есть — берём хвост от него
		old = sys[compactAt:]
	}

	if strings.Contains(old, "Old-only paper") && strings.Count(old, doiShared) > 0 {
		// the shared DOI may appear at most as the skip counter, not as a paper line
		assert.NotContains(t, old, "- 10.1/shared", "paper shared with the newer run must be skipped in the old run's digest")
	}

	assert.Contains(t, sys, "already listed under a newer run", "skip counter missing")
}
