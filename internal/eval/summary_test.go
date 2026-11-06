package eval

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// update rewrites the golden files: go test ./internal/eval -update
var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// golden compares got with testdata/<name>; with -update it writes the file
// instead, so a change of the summary format shows up as a diff in review.
func golden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))

		return
	}

	//nolint:gosec // the path is a fixed name under testdata/
	want, err := os.ReadFile(path)
	require.NoError(t, err, "missing golden file; run with -update to create it")
	assert.Equal(t, string(want), got)
}

const specNeuro = "Psychiatry & Neurology"

// goldenSummary covers every row shape: a match, a mismatch, a question
// without an expert certainty, a run without usage figures, a failed run with
// markdown-hostile text, and repeats that disagree.
func goldenSummary() *Summary {
	usage := &Usage{Calls: 4, InputTokens: 400000, OutputTokens: 5000}

	return &Summary{
		SetFile: "evals/medevidence.yaml", Date: time.Date(2026, 11, 6, 12, 30, 0, 0, time.UTC),
		TextMode: pipeline.TextHybrid, Models: "llm-model=sonnet", Repeat: 2, Questions: 3,
		Results: []Result{
			{
				ID: 10, Repeat: 1, Specialty: "Oncology & Hematology", Claim: "In women with dense breasts, " +
					"screening with mammography plus ultrasonography detects more breast cancers than screening " +
					"with mammography alone.",
				Answer: AnswerHigher, Expected: OutcomeWorks, Certainty: CertaintyHigh, Given: 4,
				Scored: true, Got: OutcomeWorks, Tally: "direct 3: positive 2, null 1", Level: LevelModerate,
				Effect: model.EffectSupports, Consensus: ConsensusBroadAgreement,
				Strength: Strength{Scored: true, Within1: true}, Reached: 4,
				Usage: usage, Report: "10-1/run/report.md",
			},
			{
				ID: 10, Repeat: 2, Specialty: "Oncology & Hematology", Claim: "In women with dense breasts, " +
					"screening with mammography plus ultrasonography detects more breast cancers than screening " +
					"with mammography alone.",
				Answer: AnswerHigher, Expected: OutcomeWorks, Certainty: CertaintyHigh, Given: 4,
				Scored: true, Got: OutcomeWorks, Tally: "direct 2: positive 2", Level: LevelStrong,
				Consensus: ConsensusEmerging, Strength: Strength{Scored: true, Exact: true, Within1: true},
				Reached: 4, Usage: usage, Report: "10-2/run/report.md",
			},
			{
				ID: 30, Repeat: 1, Specialty: specNeuro,
				Claim:  "In people with Parkinson's disease, exercise reduces fatigue severity.",
				Answer: AnswerNoDifference, Expected: OutcomeNoEffect, Certainty: CertaintyLow, Given: 2,
				Scored: true, Got: OutcomeWorks, Tally: tallyOnePositive, Level: LevelStrong,
				Effect: model.EffectSupports, Consensus: ConsensusUnknown, Strength: Strength{Scored: true},
				Reached: 2, Usage: usage,
				Report: "30-1/run/report.md",
			},
			{
				ID: 30, Repeat: 2, Specialty: specNeuro,
				Claim:  "In people with Parkinson's disease, exercise reduces fatigue severity.",
				Answer: AnswerNoDifference, Expected: OutcomeNoEffect, Certainty: CertaintyLow, Given: 2,
				Err: "stage 2-enrichment: HTTP 503 | retry later\nsecond line",
			},
			{
				ID: 230, Repeat: 1, Specialty: specNeuro,
				Claim:  "In cognitively healthy older people, computerised cognitive training improves global cognition.",
				Answer: AnswerInsufficient, Expected: OutcomeNotProven, Certainty: CertaintyNone, Given: 1,
				Scored: true, Got: OutcomeNotProven, Tally: tallyNone, Level: LevelInsufficient,
				Effect: model.EffectUnclear, Consensus: ConsensusInsufficientData, Reached: 1,
				Report: "230-1/run/report.md",
			},
		},
	}
}

func TestRenderMarkdownGolden(t *testing.T) {
	golden(t, "summary.golden.md", RenderMarkdown(goldenSummary()))
}

func TestRenderCSVGolden(t *testing.T) {
	got, err := RenderCSV(goldenSummary())
	require.NoError(t, err)
	golden(t, "summary.golden.csv", got)
}

func TestTotals(t *testing.T) {
	tot := goldenSummary().Totals()

	assert.Equal(t, 5, tot.Runs)
	assert.Equal(t, 1, tot.Failed)
	assert.Equal(t, 4, tot.Scored)
	assert.Equal(t, 3, tot.Matches)
	assert.Equal(t, 3, tot.StrengthScored)
	assert.Equal(t, 1, tot.StrengthExact)
	assert.Equal(t, 2, tot.StrengthWithin1)
	assert.Equal(t, 1, tot.Confusion[OutcomeNoEffect][OutcomeWorks])
	assert.Equal(t, 2, tot.Confusion[OutcomeWorks][OutcomeWorks])
	assert.Equal(t, 1, tot.Confusion[OutcomeNotProven][OutcomeNotProven])
	assert.Equal(t, 1, tot.Stable, "question 10 agrees with itself, 30 has a failed repeat, 230 ran once of two")
	assert.Equal(t, 12, tot.Calls)
	assert.Equal(t, 1200000, tot.InputTokens)
	assert.Equal(t, 15000, tot.OutputTokens)

	assert.Equal(t, "outcome match 3/4 (75.0%); strength exact 1/3, within one level 2/3; "+
		"1 failed run(s) not counted", tot.Line())
}

func TestTotalsLineWithoutRuns(t *testing.T) {
	assert.Equal(t, "outcome match 0/0; strength exact 0/0, within one level 0/0", (&Summary{}).Totals().Line())
}

func TestMarkdownHasBlankLinesBeforeTablesAndLists(t *testing.T) {
	md := RenderMarkdown(goldenSummary())
	lines := strings.Split(md, "\n")

	blockLine := func(l string) bool { return strings.HasPrefix(l, "|") || strings.HasPrefix(l, "- ") }

	for i, l := range lines {
		if i > 0 && blockLine(l) && !blockLine(lines[i-1]) {
			assert.Empty(t, lines[i-1], "line %d starts a table or list right after text", i+1)
		}
	}
}

func TestWriteSummaryFailsOnMissingDirectory(t *testing.T) {
	err := WriteSummary(filepath.Join(t.TempDir(), "missing"), goldenSummary())
	require.Error(t, err)
}
