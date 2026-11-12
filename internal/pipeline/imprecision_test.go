package pipeline

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	claimLower  = "lower"
	claimHigher = "higher"
)

func TestGradeInput(t *testing.T) {
	sd := 4.0

	for _, tc := range []struct {
		name    string
		pe      model.PooledEstimate
		measure string
		sd      float64
		wantErr bool
	}{
		{name: "RR", pe: model.PooledEstimate{Measure: model.MeasureRR}, measure: "ratio"},
		{name: "HR", pe: model.PooledEstimate{Measure: model.MeasureHR}, measure: "ratio"},
		{name: "OR", pe: model.PooledEstimate{Measure: model.MeasureOR}, measure: "odds_ratio"},
		{name: "SMD ignores the SD", pe: model.PooledEstimate{Measure: model.MeasureSMD, SD: &sd}, measure: "smd"},
		{name: "MD with an SD", pe: model.PooledEstimate{Measure: model.MeasureMD, SD: &sd}, measure: "md", sd: 4},
		{name: "MD without an SD", pe: model.PooledEstimate{Measure: model.MeasureMD}, measure: "md"},
		{name: "unknown measure", pe: model.PooledEstimate{Measure: "X"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.pe.Estimate, tc.pe.CILow, tc.pe.CIHigh, tc.pe.Participants = 1, 0.5, 2, 90

			in, err := gradeInput(&tc.pe)
			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.measure, string(in.Measure))
			assert.InDelta(t, tc.sd, in.SD, 1e-12)
			assert.Equal(t, 90, in.Participants)
			assert.InDelta(t, 0.5, in.CILow, 1e-12)
			assert.InDelta(t, 2, in.CIHigh, 1e-12)
		})
	}
}

func TestAssessImprecision(t *testing.T) {
	p, logs := poolPipeline(t)

	assert.Nil(t, p.assessImprecision(nil))
	assert.Empty(t, logs.String())

	got := p.assessImprecision(&model.PooledEstimate{
		Measure: model.MeasureRR, Estimate: 0.77, CILow: 0.48, CIHigh: 1.23,
	})
	require.NotNil(t, got)
	assert.Equal(t, 1, got.Levels)
	assert.Equal(t, model.TargetLittleOrNoDifference, got.Target)
	assert.Equal(t, "CI 0.48 to 1.23 includes no effect and crosses the 0.75 threshold; "+
		"the estimate 0.77 leans toward a decrease, but the interval includes no effect", got.Reason)
	assert.True(t, got.SizeChecked)
	require.NotNil(t, got.BandLow)
	assert.InDelta(t, 0.75, *got.BandLow, 1e-12)
	assert.Empty(t, logs.String())

	// a mean difference without an SD has no threshold: one level, no
	// difference seen, and the lean is named
	got = p.assessImprecision(&model.PooledEstimate{
		Measure: model.MeasureMD, Estimate: 0.87, CILow: -1.58, CIHigh: 3.32,
	})
	require.NotNil(t, got)
	assert.Equal(t, 1, got.Levels)
	assert.Equal(t, model.TargetLittleOrNoDifference, got.Target)
	assert.Contains(t, got.Reason, "the estimate 0.87 leans toward an increase")
	assert.Nil(t, got.BandLow)
	assert.Nil(t, got.BandHigh)

	// numbers that cannot be rated are logged, and the rating stays absent
	assert.Nil(t, p.assessImprecision(&model.PooledEstimate{
		Measure: model.MeasureRR, Estimate: 3, CILow: 0.5, CIHigh: 2,
	}))
	assert.Nil(t, p.assessImprecision(&model.PooledEstimate{Measure: "X", Estimate: 1, CILow: 0, CIHigh: 2}))
	assert.Equal(t, 2, bytes.Count(logs.Bytes(), []byte("imprecision not computed")))
}

// From the pooled numbers to the effect direction: an interval that includes no
// effect gives no_effect (levels 0 or 1) or unclear (levels 2) whatever the
// point estimate and the claim; one that excludes it follows the estimate.
func TestPooledIntervalDecidesTheDirection(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pe         model.PooledEstimate
		levels     int
		target     model.ImprecisionTarget
		claim      string
		said       model.EffectDirection
		want       model.EffectDirection
		reasonPart string
	}{
		{
			name:   "RR 1.29 includes no effect: no difference seen, the lean named",
			pe:     model.PooledEstimate{Measure: model.MeasureRR, Estimate: 1.29, CILow: 0.99, CIHigh: 1.68},
			levels: 1, target: model.TargetLittleOrNoDifference, claim: claimHigher,
			said: model.EffectSupports, want: model.EffectNoEffect,
			reasonPart: "the estimate 1.29 leans toward an increase, but the interval includes no effect",
		},
		{
			name:   "RR 1.29 with the limits in the wrong order and a claim of a lower value",
			pe:     model.PooledEstimate{Measure: model.MeasureRR, Estimate: 1.29, CILow: 1.68, CIHigh: 0.99},
			levels: 1, target: model.TargetLittleOrNoDifference, claim: claimLower,
			said: model.EffectOpposite, want: model.EffectNoEffect,
			reasonPart: "leans toward an increase",
		},
		{
			name:   "RR 0.67 with a very wide interval: unclear whatever the claim",
			pe:     model.PooledEstimate{Measure: model.MeasureRR, Estimate: 0.67, CILow: 0.12, CIHigh: 3.84},
			levels: 2, target: model.TargetUncertain, claim: claimLower,
			said: model.EffectSupports, want: model.EffectUnclear,
			reasonPart: "leans toward a decrease",
		},
		{
			name:   "RR 0.35 excludes no effect: the direction follows the estimate",
			pe:     model.PooledEstimate{Measure: model.MeasureRR, Estimate: 0.35, CILow: 0.18, CIHigh: 0.67},
			levels: 1, target: model.TargetLower, claim: claimLower,
			said: model.EffectUnclear, want: model.EffectSupports,
		},
		{
			name:   "RR 0.35 excludes no effect against a claim of a higher value",
			pe:     model.PooledEstimate{Measure: model.MeasureRR, Estimate: 0.35, CILow: 0.67, CIHigh: 0.18},
			levels: 1, target: model.TargetLower, claim: claimHigher,
			said: model.EffectSupports, want: model.EffectOpposite,
		},
		{
			name:   "MD without an SD, CI -1 to 3: no difference seen with the lean",
			pe:     model.PooledEstimate{Measure: model.MeasureMD, Estimate: 1, CILow: -1, CIHigh: 3},
			levels: 1, target: model.TargetLittleOrNoDifference, claim: claimLower,
			said: model.EffectUnclear, want: model.EffectNoEffect,
			reasonPart: "the estimate 1 leans toward an increase, but the interval includes no effect",
		},
		{
			name:   "SMD past both thresholds: unclear",
			pe:     model.PooledEstimate{Measure: model.MeasureSMD, Estimate: 0.1, CILow: 0.5, CIHigh: -0.6},
			levels: 2, target: model.TargetUncertain, claim: claimHigher,
			said: model.EffectNoEffect, want: model.EffectUnclear,
		},
		{
			name:   "SMD inside the trivial band: no effect, no lean",
			pe:     model.PooledEstimate{Measure: model.MeasureSMD, Estimate: 0.05, CILow: -0.1, CIHigh: 0.15},
			levels: 0, target: model.TargetLittleOrNoDifference, claim: claimHigher,
			said: model.EffectNoEffect, want: model.EffectNoEffect,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, logs := poolPipeline(t)

			imp := p.assessImprecision(&tc.pe)
			require.NotNil(t, imp)
			assert.Equal(t, tc.levels, imp.Levels, imp.Reason)
			assert.Equal(t, tc.target, imp.Target)
			assert.Contains(t, imp.Reason, tc.reasonPart)

			got, _ := p.directionFromPooled(imp, tc.claim, tc.said)
			assert.Equal(t, tc.want, got)
			assert.Empty(t, logs.String())
		})
	}
}

func TestDirectionFromPooled(t *testing.T) {
	lower := &model.Imprecision{Target: model.TargetLower}
	flat := &model.Imprecision{Target: model.TargetLittleOrNoDifference}
	uncertain := &model.Imprecision{Levels: 2, Target: model.TargetUncertain}

	for _, tc := range []struct {
		name     string
		imp      *model.Imprecision
		claim    string
		said     model.EffectDirection
		want     model.EffectDirection
		warning  string
		logsWarn bool
	}{
		{name: "no rating keeps the synthesis", claim: claimLower, said: model.EffectUnclear, want: model.EffectUnclear},
		{name: "agreement", imp: lower, claim: claimLower, said: model.EffectSupports, want: model.EffectSupports},
		{
			name: "the claim goes the other way", imp: lower, claim: "higher", said: model.EffectSupports,
			want:    model.EffectOpposite,
			warning: "effect_direction set from the pooled estimate: opposite (the synthesis said supports)",
		},
		{
			name: "little or no difference is no effect", imp: flat, claim: " Higher ", said: model.EffectSupports,
			want:    model.EffectNoEffect,
			warning: "effect_direction set from the pooled estimate: no_effect (the synthesis said supports)",
		},
		{
			name: "the synthesis was unclear", imp: lower, claim: claimLower, said: model.EffectUnclear,
			want:    model.EffectSupports,
			warning: "effect_direction set from the pooled estimate: supports (the synthesis said unclear)",
		},
		{
			name: "an uncertain target imposes unclear", imp: uncertain, claim: claimLower,
			said: model.EffectNoEffect, want: model.EffectUnclear,
			warning: "effect_direction set from the pooled estimate: unclear (the synthesis said no_effect)",
		},
		{
			name: "an uncertain target needs no claim_change", imp: uncertain,
			said: model.EffectSupports, want: model.EffectUnclear,
			warning: "effect_direction set from the pooled estimate: unclear (the synthesis said supports)",
		},
		{
			name: "little or no difference needs no claim_change", imp: flat,
			said: model.EffectSupports, want: model.EffectNoEffect,
			warning: "effect_direction set from the pooled estimate: no_effect (the synthesis said supports)",
		},
		{
			name: "an uncertain target agrees with an unclear synthesis", imp: uncertain,
			said: model.EffectUnclear, want: model.EffectUnclear,
		},
		{
			name: "a missing claim_change keeps the synthesis", imp: lower, said: model.EffectNoEffect,
			want: model.EffectNoEffect, logsWarn: true,
		},
		{
			name: "an invalid claim_change keeps the synthesis", imp: lower, claim: "same", said: model.EffectOpposite,
			want: model.EffectOpposite, logsWarn: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, logs := poolPipeline(t)

			got, warning := p.directionFromPooled(tc.imp, tc.claim, tc.said)

			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.warning, warning)
			assert.Equal(t, tc.logsWarn, logs.Len() > 0, logs.String())
		})
	}
}

func TestSynthesisInputCarriesTheImprecision(t *testing.T) {
	p, _ := poolPipeline(t)

	st := poolState(counts(10, 100, 20, 100), counts(11, 100, 21, 100))
	st.Pooled = p.poolEffects(st)

	without, err := synthesisUserPrompt(st)
	require.NoError(t, err)
	assert.NotContains(t, without, `"imprecision"`)

	st.Imprecision = p.assessImprecision(st.Pooled)
	require.NotNil(t, st.Imprecision)

	with, err := synthesisUserPrompt(st)
	require.NoError(t, err)
	assert.Contains(t, with, `"imprecision":{"levels":0,"target":"lower"`)
	assert.Contains(t, with, `"outcome":"outcome"`, "the pooled estimate names what it measures")
}

func TestSynthesisPromptSplitsTheJudgments(t *testing.T) {
	assert.Contains(t, synthesisSystemPrompt, `- claim_change: "lower" | "higher"`)
	assert.Contains(t, synthesisSystemPrompt, "when the input has imprecision, the pipeline computed it")
	assert.Contains(t, synthesisSystemPrompt, "do not judge imprecision again")
	assert.Contains(t, synthesisSystemPrompt, "little_or_no_difference = no effect")
	assert.Contains(t, synthesisSystemPrompt, "When imprecision is absent:")
	assert.Contains(t, synthesisSystemPrompt, `target "uncertain" = the evidence cannot tell`)
	assert.Contains(t, synthesisSystemPrompt,
		"When the pooled CI includes no effect, the pipeline does not take a direction")
	assert.Contains(t, synthesisSystemPrompt, "you may describe the lean")
	assert.NotContains(t, synthesisSystemPrompt, "undetermined")
	assert.Contains(t, synthesisSystemPrompt, "AS MEASURED in pooled_estimate.outcome")
	assert.Contains(t, synthesisSystemPrompt, `"children still in pain" → lower`)
	assert.Contains(t, synthesisSystemPrompt, "Without a pooled_estimate it is still required")
}

// claimReply is the synthesis answer of effectSynthesisReply with a claim_change.
func claimReply(claimChange string) string {
	if claimChange == "" {
		return effectSynthesisReply
	}

	return `{"verdict":"moderate","verdict_rationale":"pooled estimate drives it",` +
		`"effect_direction":"Supports","claim_change":"` + claimChange + `",` +
		`"consensus_status":"broad_agreement","consensus":"leans positive"}`
}

// The whole run: the pooled RR (about 0.53, lower) fixes the direction by the
// change the claim states; the code's word wins over the judge's, with a
// report warning that records both.
func TestRunSetsTheEffectDirectionFromThePooledEstimate(t *testing.T) {
	for _, tc := range []struct {
		name, claimChange string
		want              model.EffectDirection
		warning           bool
	}{
		{name: "the claim states a lower value", claimChange: claimLower, want: model.EffectSupports},
		{name: "the claim states a higher value", claimChange: "higher", want: model.EffectOpposite, warning: true},
		{name: "no claim_change keeps the judge's word", want: model.EffectSupports},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, llm := runFakes(t, effectBatchReply, claimReply(tc.claimChange))

			st, dir, err := p.Run(t.Context(), Options{
				DOIFile: doiList(t, listDOIA, listDOIB), RunDir: filepath.Join(t.TempDir(), "run"), TextMode: TextLean,
			})
			require.NoError(t, err)

			require.NotNil(t, st.Imprecision)
			assert.Equal(t, 0, st.Imprecision.Levels)
			assert.Equal(t, model.TargetLower, st.Imprecision.Target)
			assert.Contains(t, llm.prompts[1], `"imprecision":{"levels":0,"target":"lower"`)

			require.NotNil(t, st.Report)
			assert.Equal(t, tc.want, st.Report.EffectDirection)
			assert.Equal(t, st.Imprecision, st.Report.Imprecision)

			if tc.warning {
				assert.Equal(t, []string{
					"effect_direction set from the pooled estimate: opposite (the synthesis said supports)",
				}, st.Report.Warnings)
			} else {
				assert.Empty(t, st.Report.Warnings)
			}

			//nolint:gosec // reads a file the run just wrote into the test's temp directory
			report, err := os.ReadFile(filepath.Join(dir, "report.md"))
			require.NoError(t, err)
			assert.Contains(t, string(report), "**Imprecision (computed):** not rated down — CI ")

			loaded, err := LoadState(dir)
			require.NoError(t, err)
			assert.Equal(t, st.Imprecision, loaded.Imprecision, "the rating round-trips through the artifact")
			assert.Equal(t, st.Imprecision, loaded.Report.Imprecision)
		})
	}
}

// An artifact written before the rating existed loads without it and renders
// as it did.
func TestStateWithoutImprecisionLoads(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "4-synthesis.json"),
		[]byte(`{"question":"q","papers":[],"pooled_estimate":{"method":"single_study","measure":"RR","estimate":0.5,`+
			`"ci_low":0.3,"ci_high":0.8,"k":1,"studies":["10.1/a"]},`+
			`"report":{"question":"q","generated_at":"2026-11-01T00:00:00Z","verdict":"weak","verdict_rationale":"r",`+
			`"evidence_matrix":[]}}`), 0o600))

	st, err := LoadState(dir)
	require.NoError(t, err)
	assert.Nil(t, st.Imprecision)
	assert.Nil(t, st.Report.Imprecision)
	assert.NotContains(t, RenderMarkdown(st), "Imprecision")
}
