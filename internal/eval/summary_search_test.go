package eval

import (
	"encoding/csv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/pipeline"
)

// searchSummary has two scored runs with recall and one failed run.
func searchSummary() *Summary {
	scored := func(id int, rec Recall) Result {
		return Result{
			ID: id, Repeat: 1, Claim: "A", Answer: AnswerHigher, Expected: OutcomeWorks, Given: rec.Total,
			Scored: true, Got: OutcomeWorks, Reached: rec.Corpus, Recall: &rec,
		}
	}

	return &Summary{
		SetFile: "evals/medevidence.yaml", Date: testNow, Mode: ModeSearch, TextMode: pipeline.TextHybrid,
		Models: "defaults", Repeat: 1, Questions: 3,
		Results: []Result{
			scored(1, Recall{Total: 4, Pool: 3, Screened: 2, Corpus: 1}),
			scored(2, Recall{Total: 2, Pool: 2, Screened: 2, Corpus: 2}),
			{ID: 3, Repeat: 1, Claim: "A", Answer: AnswerHigher, Expected: OutcomeWorks, Given: 5, Err: "boom"},
		},
	}
}

func runLine(t *testing.T, md, id string) string {
	t.Helper()

	for l := range strings.SplitSeq(md, "\n") {
		if strings.HasPrefix(l, "| "+id+" | 1 |") {
			return l
		}
	}

	require.FailNow(t, "no run row", id)

	return ""
}

func TestRenderMarkdownSearchMode(t *testing.T) {
	md := RenderMarkdown(searchSummary())

	assert.Contains(t, md, "found by the search")
	assert.Contains(t, md, "| Strength ±1 | Recall pool | Recall screened | Recall corpus | Studies in report |",
		"the recall columns take the place of the given studies")
	assert.NotContains(t, md, "Studies given")
	assert.Contains(t, md, "- Search recall, in the discovery pool: 5/6 (83.3%)\n")
	assert.Contains(t, md, "- Search recall, kept by the screening: 4/6 (66.7%)\n")
	assert.Contains(t, md, "- Search recall, in the corpus of the critic: 3/6 (50.0%)\n")

	assert.Contains(t, runLine(t, md, "1"), "| 3/4 | 2/4 | 1/4 |")
	assert.Contains(t, runLine(t, md, "2"), "| 2/2 | 2/2 | 2/2 |")
	assert.Contains(t, runLine(t, md, "3"), "| - | - | - |", "a failed run has no recall, outside the totals")
}

func TestRenderCSVSearchMode(t *testing.T) {
	got, err := RenderCSV(searchSummary())
	require.NoError(t, err)

	rows, err := csv.NewReader(strings.NewReader(got)).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 4)

	assert.Equal(t, columns(true), rows[0])
	assert.Len(t, rows[1], len(rows[0]))

	at := indexOf(t, rows[0], "Recall pool")
	assert.Equal(t, []string{"3/4", "2/4", "1/4"}, rows[1][at:at+3])
	assert.Equal(t, []string{"", "", ""}, rows[3][at:at+3], "unknown is an empty cell in the CSV")
	assert.NotContains(t, rows[0], "Studies given")
}

func indexOf(t *testing.T, row []string, name string) int {
	t.Helper()

	for i, c := range row {
		if c == name {
			return i
		}
	}

	require.FailNow(t, "no column", name)

	return -1
}

func TestTotalsLineSearchMode(t *testing.T) {
	tot := searchSummary().Totals()

	assert.True(t, tot.Search)
	assert.Equal(t, Recall{Total: 6, Pool: 5, Screened: 4, Corpus: 3}, tot.Recall, "the failed run is outside")
	assert.Equal(t, "outcome match 2/2 (100.0%); strength exact 0/0, within one level 0/0; "+
		"search recall: pool 5/6 (83.3%), screened 4/6 (66.7%), corpus 3/6 (50.0%); 1 failed run(s) not counted",
		tot.Line())
}

func TestTotalsLineSearchModeWithoutRuns(t *testing.T) {
	line := (&Summary{Mode: ModeSearch}).Totals().Line()

	assert.Contains(t, line, "search recall: pool 0/0, screened 0/0, corpus 0/0")
}

func TestDOIModeSummaryHasNoSearchParts(t *testing.T) {
	s := goldenSummary()

	assert.Equal(t, columns(false), runColumns)
	assert.NotContains(t, RenderMarkdown(s), "ecall")
	assert.NotContains(t, s.Totals().Line(), "recall")
}
