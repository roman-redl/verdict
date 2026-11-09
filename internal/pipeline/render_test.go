package pipeline

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// update rewrites the golden files: go test ./internal/pipeline -update
var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// golden compares got with testdata/<name>; with -update it writes the file
// instead. The files are the reference output of the report generators: a
// change shows up as a readable diff in review.
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

const (
	reportQuestion = "Does creatine improve cognitive performance in healthy adults?"
	// longTitle is long enough to be cut in the matrix and carries a pipe,
	// which would break a markdown table cell.
	longTitle  = "A very long title that has to be shortened because tables break on long cells | and pipes"
	doiTrial   = "10.1000/a"
	doiCohort  = "10.1000/b"
	doiElderly = "10.1000/c"
)

// reportState is a finished run with every feature the renderers show: a
// retracted paper, a full text, a contradicted study with Scite details,
// critique concerns with markdown-hostile characters, a data warning, usage.
func reportState() *State {
	return &State{
		Question: reportQuestion,
		Warnings: []string{"Scite unavailable, citation analytics incomplete: HTTP 503"},
		Papers:   []model.Paper{{DOI: doiTrial}, {DOI: doiCohort}, {DOI: doiElderly}},
		Enriched: []model.EnrichedPaper{
			{
				DOI: doiTrial, Title: "Creatine and memory: a randomized trial", Year: 2021,
				StudyDesign: designRCT, SampleSize: 120, Abstract: "Background: a double-blind trial in 120 adults.",
				Tally:    &model.CitationTally{Total: 40, Supporting: 12, Contradicting: 3},
				FullText: "Methods and results...", ContradictingDOIs: []string{criticDOI},
			},
			{
				DOI: doiCohort, Title: longTitle, Year: 2018, StudyDesign: "cohort", CitedByCount: 7,
				Flags: model.RedFlags{Retracted: true, Reason: "Crossref/retraction-watch: retraction notice 10.1/n"},
			},
			{DOI: doiElderly, Title: "Creatine in the elderly", Year: 2023},
		},
		Critiques: []model.Critique{
			{
				DOI: doiTrial, DesignQuality: qualityHigh, SampleAdequacy: "adequate", SampleSizeReported: 118,
				Relevance: relevanceDirect, ResultDirection: directionPositive,
				Concerns: []string{"short follow-up", "single center | one site"},
			},
			{DOI: doiCohort, DesignQuality: qualityLow, Relevance: relevanceIndirect, ResultDirection: directionNull},
			{DOI: doiElderly, DesignQuality: qualityModerate, Relevance: relevanceDirect, ResultDirection: directionMixed},
		},
		Report: &model.Report{
			Question:         reportQuestion,
			GeneratedAt:      time.Date(2026, 11, 1, 12, 30, 0, 0, time.UTC),
			Verdict:          qualityModerate,
			VerdictRationale: "10.1000/a is a well-run RCT; 10.1000/c disagrees on direction.",
			ConsensusStatus:  "contested",
			Consensus:        "Most direct studies lean positive (10.1000/a), one is mixed (10.1000/c).",
			EffectDirection:  model.EffectSupports,
			PooledEstimate: &model.PooledEstimate{
				Method: model.PoolRandomEffects, Measure: model.MeasureSMD, Estimate: -0.22, CILow: -0.40, CIHigh: -0.03,
				K: 3, Participants: 1234, I2: new(41.6), Studies: []string{doiTrial, doiElderly},
				Outcome: "Memory score (12 weeks)",
			},
			Imprecision: &model.Imprecision{
				Levels: 0, Target: model.TargetLower, SizeChecked: true,
				Reason: "CI -0.4 to -0.03 excludes no effect; 1234 participants, " +
					"reaching the optimal information size of about 800",
			},
			Matrix: []model.MatrixRow{
				{
					DOI: doiTrial, Title: "Creatine and memory: a randomized trial", Year: 2021, Design: designRCT,
					SampleSize: 118, Supporting: 12, Contradicting: 3, TotalCitations: 40, Direction: directionPositive,
					DesignQuality: qualityHigh, Relevance: relevanceDirect, Note: "the pivotal trial, short follow-up",
				},
				{
					DOI: doiCohort, Title: longTitle, Year: 2018, Design: "cohort", TotalCitations: 7,
					Direction: directionNull, DesignQuality: qualityLow, Relevance: relevanceIndirect, Note: "retracted",
				},
				{
					Title: "Creatine in the elderly, a title with a comma", Year: 2023,
					Direction: directionMixed, DesignQuality: qualityModerate, Relevance: relevanceDirect,
				},
			},
			Contradictions: []string{"10.1000/a vs 10.1000/c on direction"},
			Gaps:           []string{"no study beyond 12 weeks"},
			Warnings:       []string{"Scite unavailable, citation analytics incomplete: HTTP 503"},
		},
		Cost: &CostReport{
			Critique:  &StageCost{Calls: 2, InputTokens: 9000, OutputTokens: 700},
			Synthesis: &StageCost{Calls: 1, InputTokens: 4000, OutputTokens: 500},
		},
	}
}

func TestRenderMarkdownGolden(t *testing.T) {
	golden(t, "report.golden.md", RenderMarkdown(reportState()))
}

func TestRenderContextGolden(t *testing.T) {
	golden(t, "context.golden.md", RenderContext(reportState()))
}

func TestRenderCSVGolden(t *testing.T) {
	got, err := RenderCSV(reportState().Report)
	require.NoError(t, err)
	golden(t, "matrix.golden.csv", got)
}

// A partial run (stopped after discovery) still renders a digest.
func TestRenderContextPartialRunGolden(t *testing.T) {
	st := &State{
		Question: "q?",
		Papers:   []model.Paper{{DOI: doiTrial, Title: "First", Year: 2020}, {Title: "No DOI", Year: 2019}},
	}

	golden(t, "context-partial.golden.md", RenderContext(st))
}

func TestRenderPICOGolden(t *testing.T) {
	golden(t, "pico.golden.md", RenderPICO(&PICO{
		Question: reportQuestion, Population: "healthy adults",
		Intervention: "creatine supplementation", Outcome: "cognitive performance",
		KeywordQuery:    "creatine cognition randomized",
		SemanticQueries: []string{"does creatine improve memory", "creatine and executive function"},
		Notes:           "sleep-deprived populations may deserve a separate run",
	}))
}

func TestRenderBriefGolden(t *testing.T) {
	golden(t, "brief.golden.md", RenderBrief(&Brief{
		Problem: "Which creatine form to buy?", Decision: "buy or skip",
		Tasks: []BriefTask{{Task: "Compare prices", Kind: "shopping", Note: "per 5 g dose"}},
		Questions: []BriefQuestion{
			{Question: "Does creatine improve cognition?", Why: "the main benefit claimed", Priority: "high"},
		},
		Claims:  []BriefClaim{{Claim: "monohydrate is the best studied", Source: "forum", Check: "verdict"}},
		Warning: "talk to a physician before dosing",
	}))
}

// Cells that would break a markdown table are made safe.
func TestMarkdownCellEscapes(t *testing.T) {
	assert.Equal(t, `a\|b c`, mdCell("a|b\nc"))
	assert.Equal(t, "a/b c", shortTitle("a|b\nc", 10))
	assert.Equal(t, "abcd…", shortTitle("abcdefghij", 5))
	assert.Equal(t, "—", numOrDash(0))
	assert.Equal(t, "12", numOrDash(12))
	assert.Equal(t, "—", orDash("  "))
}

func TestPooledText(t *testing.T) {
	i2 := new(0.0)

	for _, tc := range []struct {
		name string
		pe   model.PooledEstimate
		want string
	}{
		{
			"ratio with all figures",
			model.PooledEstimate{
				Method: model.PoolRandomEffects, Measure: model.MeasureRR, Estimate: 0.4896,
				CILow: 0.3449, CIHigh: 0.6951, K: 13, Participants: 400000, I2: new(92.12),
			},
			"RR 0.49 (95% CI 0.34 to 0.70), 13 studies, 400000 participants, I² = 92%",
		},
		{
			"mean difference with a unit, no participants, no heterogeneity",
			model.PooledEstimate{
				Method: model.PoolRandomEffects, Measure: model.MeasureMD, Unit: unitMmHg, Estimate: -2.1,
				CILow: -3.04, CIHigh: -1.16, K: 2, I2: i2,
			},
			"MD -2.10 mmHg (95% CI -3.04 to -1.16), 2 studies, I² = 0%",
		},
		{
			"a single study, a small effect keeps three decimals",
			model.PooledEstimate{
				Method: model.PoolSingleStudy, Measure: model.MeasureSMD, Estimate: 0.045,
				CILow: -0.31, CIHigh: 0.4, K: 1, Participants: 90,
			},
			"SMD 0.045 (95% CI -0.310 to 0.400), 1 study, 90 participants",
		},
		{
			"with the outcome",
			model.PooledEstimate{
				Method: model.PoolSingleStudy, Measure: model.MeasureRR, Estimate: 0.28,
				CILow: 0.11, CIHigh: 0.7, K: 1, Outcome: "Children still in pain (48 hours)",
			},
			"RR 0.28 (95% CI 0.11 to 0.70), 1 study; outcome: Children still in pain (48 hours)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, pooledText(&tc.pe))
		})
	}

	assert.Contains(t, pooledLabel(&model.PooledEstimate{Method: model.PoolSingleStudy}), "not pooled")
	assert.Contains(t, pooledLabel(&model.PooledEstimate{Method: model.PoolRandomEffects}), "DerSimonian-Laird")
	assert.Empty(t, pooledLine(nil))
}

// A report from before the effect fields renders as it did.
func TestRenderWithoutEffectFields(t *testing.T) {
	st := reportState()
	st.Report.EffectDirection, st.Report.PooledEstimate, st.Report.Imprecision = "", nil, nil

	md := RenderMarkdown(st)
	assert.NotContains(t, md, "Effect direction")
	assert.NotContains(t, md, "Pooled effect")
	assert.NotContains(t, md, "Imprecision")
	assert.NotContains(t, RenderContext(st), "Effect direction")
	assert.NotContains(t, RenderContext(st), "Pooled")
}

func TestImprecisionText(t *testing.T) {
	assert.Equal(t, "not rated down — r", imprecisionText(&model.Imprecision{Reason: "r"}))
	assert.Equal(t, "rated down 1 level — r", imprecisionText(&model.Imprecision{Levels: 1, Reason: "r"}))
	assert.Equal(t, "rated down 2 levels — r", imprecisionText(&model.Imprecision{Levels: 2, Reason: "r"}))
}

func TestEffectDirectionLabels(t *testing.T) {
	assert.Equal(t, "supports the claim", effectDirectionLabel(model.EffectSupports))
	assert.Equal(t, "no meaningful effect", effectDirectionLabel(model.EffectNoEffect))
	assert.Equal(t, "opposite effect", effectDirectionLabel(model.EffectOpposite))
	assert.Equal(t, "cannot be stated", effectDirectionLabel(model.EffectUnclear))
	assert.Equal(t, "odd", effectDirectionLabel("odd"))
	assert.Empty(t, effectDirectionSentence(""))
	assert.Equal(t, " Effect direction: opposite effect.", effectDirectionSentence(model.EffectOpposite))
}

func TestLabels(t *testing.T) {
	assert.Equal(t, "STRONG — well-established evidence", verdictLabel("Strong"))
	assert.Equal(t, "ODD", verdictLabel("odd"))
	assert.Equal(t, "broad agreement", consensusLabel("broad_agreement"))
	assert.Equal(t, "—", consensusLabel(""))
	assert.Equal(t, "something else", consensusLabel("something else"))
}
