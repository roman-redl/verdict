package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

func TestBuildMatrix(t *testing.T) {
	st := &State{
		Enriched: []model.EnrichedPaper{
			{
				DOI: doiA, Title: "Trial A", Year: 2020, CitedByCount: 41,
				Tally: &model.CitationTally{Total: 39, Supporting: 5, Contradicting: 2},
			},
			{DOI: doiB, Title: "Trial B", Year: 2023, CitedByCount: 7},
		},
		Critiques: []model.Critique{
			{DOI: doiA, DesignQuality: qualityHigh, Relevance: relevanceDirect, ResultDirection: directionPositive},
			{
				DOI: doiB, SampleSizeReported: 60, DesignQuality: qualityLow, Relevance: relevanceDirect,
				ResultDirection: directionNull, Concerns: []string{"no control group", "4-week follow-up", "single center"},
			},
		},
	}
	notes := map[string]string{"https://doi.org/10.1/A": "LLM note for A"} // non-normalized key
	rows := buildMatrix(st, notes)

	require.Len(t, rows, 2)

	a := rows[0]
	assert.Equal(t, 5, a.Supporting)
	assert.Equal(t, 2, a.Contradicting)
	assert.Equal(t, 39, a.TotalCitations)

	assert.Equal(t, "LLM note for A", a.Note, "DOI keys must normalize")

	assert.Equal(t, directionPositive, a.Direction)

	b := rows[1]
	assert.Equal(t, 0, b.Supporting, "no tally must yield 0 support")

	assert.Equal(t, 7, b.TotalCitations, "no tally must fall back to OpenAlex cited_by")

	assert.Equal(t, 60, b.SampleSize, "sample size comes from the critique")

	assert.Equal(t, "no control group; 4-week follow-up", b.Note, "note fallback is the first 2 concerns")
}

func TestCritiquesFor(t *testing.T) {
	// Index-aligned (the normal case): empty DOIs must not collide.
	st := &State{
		Enriched: []model.EnrichedPaper{
			{Title: "No DOI one"},
			{Title: "No DOI two"},
		},
		Critiques: []model.Critique{
			{Title: "No DOI one", ResultDirection: directionPositive},
			{Title: "No DOI two", ResultDirection: directionNegative},
		},
	}

	crits := critiquesFor(st)
	require.Equal(t, directionPositive, crits[0].ResultDirection, "index alignment broken for DOI-less papers")
	require.Equal(t, directionNegative, crits[1].ResultDirection, "index alignment broken for DOI-less papers")

	// Mismatched lengths (hand-edited artifact): DOI fallback, empty keys ignored.
	st2 := &State{
		Enriched: []model.EnrichedPaper{
			{DOI: "10.1/x"},
			{DOI: "10.1/y"},
		},
		Critiques: []model.Critique{
			{DOI: "", ResultDirection: directionMixed},
			{DOI: "10.1/y", ResultDirection: directionNull},
		},
	}

	crits2 := critiquesFor(st2)
	assert.Equal(t, directionNull, crits2[1].ResultDirection, "DOI fallback: want y -> null")

	assert.False(t, hasCritique(crits2[0]), "empty-DOI critique must not attach to paper x")
}

func TestRenderMarkdownEscapesPipes(t *testing.T) {
	st := &State{
		Report: &model.Report{
			Verdict: "weak",
			Matrix: []model.MatrixRow{
				{DOI: "10.1/p", Title: "Pipe study", Design: "RCT|pilot", Note: "concerns: a | b"},
			},
		},
	}

	out := RenderMarkdown(st)
	require.Contains(t, out, `RCT\|pilot`, "pipes must be escaped in table cells")
	require.Contains(t, out, `a \| b`, "pipes must be escaped in table cells")

	require.Contains(t, out, "| Direction |", "Direction column missing")
}
