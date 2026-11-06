package pipeline

import (
	"bytes"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	unitMmHg = "mmHg"
	study1   = "10.1/s1"
	study2   = "10.1/s2"
)

// counts is a binary outcome: events/total in the intervention and control arms.
func counts(e1, n1, e2, n2 float64) *model.Effect {
	return &model.Effect{
		Outcome:      "outcome",
		Intervention: &model.Arm{Events: new(e1), Total: new(n1)},
		Control:      &model.Arm{Events: new(e2), Total: new(n2)},
	}
}

// means is a continuous outcome: 10 against 8 with the given SD in both arms
// of 20 participants (Cohen's d = 1 for an SD of 2).
func means(sd float64, unit string) *model.Effect {
	return &model.Effect{
		Unit:         unit,
		Intervention: &model.Arm{Mean: new(10.0), SD: new(sd), N: new(20.0)},
		Control:      &model.Arm{Mean: new(8.0), SD: new(sd), N: new(20.0)},
	}
}

// reported is an estimate with its 95% interval.
func reported(measure string, value, low, high float64) *model.Effect {
	return &model.Effect{Measure: measure, Value: new(value), CILow: new(low), CIHigh: new(high)}
}

// withLevel states the confidence level, in percent, of a reported interval.
func withLevel(ef *model.Effect, level float64) *model.Effect {
	ef.CILevel = new(level)
	return ef
}

// poolState is a corpus of direct studies, one per effect (nil = no numbers).
func poolState(effects ...*model.Effect) *State {
	st := &State{}

	for i, ef := range effects {
		doi := fmt.Sprintf("10.1/s%d", i+1)

		st.Enriched = append(st.Enriched, model.EnrichedPaper{})
		st.Enriched[i].DOI, st.Enriched[i].Title = doi, doi
		st.Critiques = append(st.Critiques, model.Critique{DOI: doi, Relevance: relevanceDirect, Effect: ef})
	}

	return st
}

func poolPipeline(t *testing.T) (*Pipeline, *bytes.Buffer) {
	t.Helper()

	var logs bytes.Buffer

	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	return newTest(t, Settings{}, Clients{}, log), &logs
}

func TestPoolEffectsFromCounts(t *testing.T) {
	p, _ := poolPipeline(t)

	// two identical trials, 10/100 vs 20/100: RR 0.5, SE of each log RR
	// sqrt(0.13), pooled SE sqrt(0.13/2), CI 0.5 * exp(+-1.96 * 0.25495)
	pe := p.poolEffects(poolState(counts(10, 100, 20, 100), counts(10, 100, 20, 100)))

	require.NotNil(t, pe)
	assert.Equal(t, model.PoolRandomEffects, pe.Method)
	assert.Equal(t, model.MeasureRR, pe.Measure)
	assert.Equal(t, 2, pe.K)
	assert.Equal(t, 400, pe.Participants)
	assert.InDelta(t, 0.5, pe.Estimate, 1e-9)
	assert.InDelta(t, 0.3034, pe.CILow, 0.0001)
	assert.InDelta(t, 0.8241, pe.CIHigh, 0.0001)
	require.NotNil(t, pe.I2)
	assert.InDelta(t, 0, *pe.I2, 1e-9)
	assert.Equal(t, []string{study1, study2}, pe.Studies)
	assert.Empty(t, pe.Unit)
}

// Each trial is non-significant, the pooled interval is not: the case the
// pooling exists for.
func TestPoolEffectsSignificantFromNonSignificantTrials(t *testing.T) {
	p, _ := poolPipeline(t)

	trials := []*model.Effect{counts(14, 120, 22, 118), counts(20, 200, 31, 198), counts(25, 150, 36, 152)}
	for _, ef := range trials {
		single := p.poolEffects(poolState(ef))
		require.NotNil(t, single)
		assert.Greater(t, single.CIHigh, 1.0, "one trial alone spans no effect")
	}

	pe := p.poolEffects(poolState(trials...))
	require.NotNil(t, pe)
	assert.Equal(t, 3, pe.K)
	assert.Less(t, pe.CIHigh, 1.0, "the pooled interval excludes no effect")
	assert.Less(t, pe.Estimate, 1.0)
}

func TestPoolEffectsSingleStudy(t *testing.T) {
	p, _ := poolPipeline(t)

	pe := p.poolEffects(poolState(counts(10, 689, 18, 897)))

	require.NotNil(t, pe)
	assert.Equal(t, model.PoolSingleStudy, pe.Method)
	assert.Equal(t, 1, pe.K)
	assert.Nil(t, pe.I2)
	assert.InDelta(t, 0.72, pe.Estimate, 0.005)
	assert.InDelta(t, 0.34, pe.CILow, 0.005)
	assert.InDelta(t, 1.56, pe.CIHigh, 0.005)
	assert.Equal(t, 1586, pe.Participants)
}

func TestPoolEffectsNothingToPool(t *testing.T) {
	p, _ := poolPipeline(t)

	assert.Nil(t, p.poolEffects(&State{}))
	assert.Nil(t, p.poolEffects(poolState(nil, nil)))
}

// A zero cell gets the continuity correction; a trial without events in
// either arm is left out (and said so).
func TestPoolEffectsZeroCells(t *testing.T) {
	p, logs := poolPipeline(t)

	pe := p.poolEffects(poolState(counts(0, 50, 5, 50), counts(0, 40, 0, 40), counts(1, 60, 6, 60)))

	require.NotNil(t, pe)
	assert.Equal(t, 2, pe.K, "the double-zero trial is skipped")
	assert.Equal(t, []string{study1, "10.1/s3"}, pe.Studies)
	assert.Contains(t, logs.String(), study2)
	assert.Contains(t, logs.String(), "no events")
	assert.Less(t, pe.Estimate, 1.0)
}

func TestPoolEffectsMeanDifferenceInOneUnit(t *testing.T) {
	p, _ := poolPipeline(t)

	// MD 2, SE of each sqrt(0.4); pooled SE sqrt(0.2) = 0.44721
	pe := p.poolEffects(poolState(means(2, "points"), means(2, " Points ")))

	require.NotNil(t, pe)
	assert.Equal(t, model.MeasureMD, pe.Measure)
	assert.Equal(t, "points", pe.Unit)
	assert.InDelta(t, 2, pe.Estimate, 1e-9)
	assert.InDelta(t, 1.1235, pe.CILow, 0.0001)
	assert.InDelta(t, 2.8765, pe.CIHigh, 0.0001)
	assert.Equal(t, 80, pe.Participants)
}

// Differing or missing units cannot be pooled as a mean difference: Hedges' g.
func TestPoolEffectsMeansInDifferentUnitsAreStandardized(t *testing.T) {
	for name, units := range map[string][2]string{
		"different units": {unitMmHg, "points"},
		"a missing unit":  {"points", ""},
	} {
		t.Run(name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			pe := p.poolEffects(poolState(means(2, units[0]), means(2, units[1])))

			require.NotNil(t, pe)
			assert.Equal(t, model.MeasureSMD, pe.Measure)
			assert.Empty(t, pe.Unit)
			assert.InDelta(t, 1-3.0/151, pe.Estimate, 1e-9, "Hedges' g of d = 1 with 38 degrees of freedom")
		})
	}
}

func TestPoolEffectsReportedEstimates(t *testing.T) {
	p, _ := poolPipeline(t)

	pe := p.poolEffects(poolState(reported("rr", 0.80, 0.65, 0.99), reported("RR", 0.80, 0.65, 0.99)))

	require.NotNil(t, pe)
	assert.Equal(t, model.MeasureRR, pe.Measure)
	assert.InDelta(t, 0.80, pe.Estimate, 1e-9)
	assert.Equal(t, 0, pe.Participants, "the sizes are unknown")

	md := reported("MD", -2.0, -3.0, -1.0)
	md.Unit = unitMmHg
	pe = p.poolEffects(poolState(md, md))
	require.NotNil(t, pe)
	assert.Equal(t, model.MeasureMD, pe.Measure)
	assert.Equal(t, unitMmHg, pe.Unit)

	pe = p.poolEffects(poolState(reported("SMD", -0.2, -0.4, -0.04)))
	require.NotNil(t, pe)
	assert.Equal(t, model.MeasureSMD, pe.Measure)
}

// The interval's own level sets the quantile: HR 1.183 (97.2% CI 0.989 to
// 1.416) has a log-SE of (ln 1.416 - ln 0.989) / (2 * 2.197).
func TestPoolEffectsReportedIntervalLevel(t *testing.T) {
	p, _ := poolPipeline(t)

	ef := reported("HR", 1.183, 0.989, 1.416)
	ef.CILevel = new(97.2)

	pe := p.poolEffects(poolState(ef))
	require.NotNil(t, pe)
	assert.InDelta(t, 1.183, pe.Estimate, 1e-9)

	// a single study's interval is the 95% one for SE: recover it from the log-scale width
	se := (math.Log(pe.CIHigh) - math.Log(pe.CILow)) / (2 * 1.959964)
	assert.InDelta(t, (math.Log(1.416)-math.Log(0.989))/(2*2.197), se, 0.0005)

	// the same bounds read as a 95% interval imply a larger SE (a smaller z), so a wider result
	at95 := p.poolEffects(poolState(reported("HR", 1.183, 0.989, 1.416)))
	require.NotNil(t, at95)
	assert.Greater(t, at95.CIHigh-at95.CILow, pe.CIHigh-pe.CILow)

	// and as a 90% interval, a larger one still
	ninety := reported("HR", 1.183, 0.989, 1.416)
	ninety.CILevel = new(90.0)

	pe90 := p.poolEffects(poolState(ninety))
	require.NotNil(t, pe90)
	assert.Greater(t, pe90.CIHigh-pe90.CILow, at95.CIHigh-at95.CILow)
}

// An estimate of the comparator versus the intervention is flipped: a ratio to
// 1/value (1/high .. 1/low), a difference to -value (-high .. -low); the flip
// is logged.
func TestPoolEffectsReversedEstimate(t *testing.T) {
	p, logs := poolPipeline(t)

	hr := reported("HR", 1.2, 1.03, 1.4)
	hr.Reversed = true

	pe := p.poolEffects(poolState(hr))
	require.NotNil(t, pe)
	assert.InDelta(t, 0.8333, pe.Estimate, 1e-4)
	assert.Contains(t, logs.String(), "reversed to intervention vs control")

	// the converted bounds themselves (a single study's pooled interval is rebuilt from its SE)
	v, lo, hi, err := reverseEstimate(model.MeasureHR, 1.2, 1.03, 1.4)
	require.NoError(t, err)
	assert.InDelta(t, 0.8333, v, 1e-4)
	assert.InDelta(t, 0.7143, lo, 1e-4)
	assert.InDelta(t, 0.9709, hi, 1e-4)

	v, lo, hi, err = reverseEstimate(model.MeasureMD, 2, 1, 3)
	require.NoError(t, err)
	assert.InDelta(t, -2, v, 1e-9)
	assert.InDelta(t, -3, lo, 1e-9)
	assert.InDelta(t, -1, hi, 1e-9)

	md := reported("MD", 2, 1, 3)
	md.Unit = unitMmHg
	md.Reversed = true

	pe = p.poolEffects(poolState(md))
	require.NotNil(t, pe)
	assert.InDelta(t, -2, pe.Estimate, 1e-9)
	assert.InDelta(t, -3, pe.CILow, 1e-9)
	assert.InDelta(t, -1, pe.CIHigh, 1e-9)

	// pooled with another study, a reversed one equals the same study entered in the right order
	other := reported("HR", 0.7, 0.5, 0.98)
	right := p.poolEffects(poolState(other, reported("HR", 1/1.2, 1/1.4, 1/1.03)))
	flipped := p.poolEffects(poolState(other, hr))

	require.NotNil(t, right)
	require.NotNil(t, flipped)
	assert.InDelta(t, right.Estimate, flipped.Estimate, 1e-9)
	assert.InDelta(t, right.CILow, flipped.CILow, 1e-9)
	assert.InDelta(t, right.CIHigh, flipped.CIHigh, 1e-9)
	assert.Equal(t, 2, flipped.K)

	// the flag applies to a reported estimate only: the arms' numbers stay as they are
	arms := counts(10, 100, 20, 100)
	arms.Reversed = true
	assert.Equal(t, p.poolEffects(poolState(counts(10, 100, 20, 100))), p.poolEffects(poolState(arms)))

	// a ratio that cannot be inverted is not pooled
	bad := reported("HR", 1.2, 0, 1.4)
	bad.Reversed = true
	assert.Nil(t, p.poolEffects(poolState(bad)))
}

// A binary outcome given only as percentages of the arms: the count is
// computed in Go, and the conversion is logged.
func TestPoolEffectsEventsFromPercent(t *testing.T) {
	p, logs := poolPipeline(t)

	// 33% of 300 = 99 against 20% of 300 = 60: the same as counts(99, 300, 60, 300)
	ef := &model.Effect{
		Intervention: &model.Arm{Percent: new(33.0), Total: new(300.0)},
		Control:      &model.Arm{Percent: new(20.0), Total: new(300.0)},
	}

	got := p.poolEffects(poolState(ef))
	want := p.poolEffects(poolState(counts(99, 300, 60, 300)))

	require.NotNil(t, got)
	require.NotNil(t, want)
	assert.Equal(t, want, got)
	assert.Equal(t, 600, got.Participants)
	assert.Contains(t, logs.String(), "derived from percentages")
	assert.Contains(t, logs.String(), "intervention 33% of 300 = 99 events")

	// rounding: 12.5% of 30 = 3.75 -> 4
	rounded := &model.Effect{
		Intervention: &model.Arm{Percent: new(12.5), Total: new(30.0)},
		Control:      &model.Arm{Events: new(9.0), Total: new(30.0)},
	}
	assert.Equal(t, p.poolEffects(poolState(counts(4, 30, 9, 30))), p.poolEffects(poolState(rounded)))

	// given events win over a percentage
	both := counts(10, 100, 20, 100)
	both.Intervention.Percent = new(99.0)
	assert.Equal(t, p.poolEffects(poolState(counts(10, 100, 20, 100))), p.poolEffects(poolState(both)))
}

// An estimate-only study counts its participants from the critique when known.
func TestPoolEffectsParticipantsFromTheStudySize(t *testing.T) {
	p, _ := poolPipeline(t)

	st := poolState(reported("HR", 0.9, 0.7, 1.1), reported("HR", 0.8, 0.6, 1.0))
	st.Enriched[0].SampleSize = 300
	st.Critiques[1].SampleSizeReported = 200

	pe := p.poolEffects(st)
	require.NotNil(t, pe)
	assert.Equal(t, 500, pe.Participants)
}

// Ratio measures are never mixed: the family with most studies is pooled,
// and equal families go by the fixed measure order.
func TestPoolEffectsDoesNotMixMeasures(t *testing.T) {
	p, logs := poolPipeline(t)

	pe := p.poolEffects(poolState(
		reported("OR", 0.7, 0.5, 0.98), reported("HR", 0.6, 0.4, 0.9), reported("OR", 0.9, 0.6, 1.3),
	))

	require.NotNil(t, pe)
	assert.Equal(t, model.MeasureOR, pe.Measure)
	assert.Equal(t, 2, pe.K)
	assert.Contains(t, logs.String(), "several measures")

	tie := p.poolEffects(poolState(reported("HR", 0.6, 0.4, 0.9), counts(10, 100, 20, 100)))
	require.NotNil(t, tie)
	assert.Equal(t, model.MeasureRR, tie.Measure, "equal families: RR comes first")
	assert.Equal(t, 1, tie.K)

	// an RR from counts and a reported RR are one family
	same := p.poolEffects(poolState(counts(10, 100, 20, 100), reported("RR", 0.6, 0.4, 0.9)))
	require.NotNil(t, same)
	assert.Equal(t, 2, same.K)
}

// Only direct, non-retracted studies are pooled.
func TestPoolEffectsSkipsIndirectAndRetracted(t *testing.T) {
	p, _ := poolPipeline(t)

	st := poolState(counts(10, 100, 20, 100), counts(10, 100, 20, 100), counts(5, 100, 25, 100), counts(1, 100, 30, 100))
	st.Critiques[2].Relevance = relevanceIndirect
	st.Enriched[3].Flags.Retracted = true

	pe := p.poolEffects(st)
	require.NotNil(t, pe)
	assert.Equal(t, []string{study1, study2}, pe.Studies)
}

// Numbers that cannot be an effect are ignored, each with a warning.
func TestPoolEffectsIgnoresBadValuesWithAWarning(t *testing.T) {
	effects := []*model.Effect{
		// more events than participants, negative events, an SD of zero
		counts(12, 10, 2, 10),
		counts(-1, 10, 2, 10),
		means(0, "points"),
		// an unknown measure, no interval, the value outside its interval, a non-positive ratio, an MD without a unit
		reported("RRR", 0.8, 0.6, 1.0),
		{Measure: "RR", Value: new(0.8)},
		reported("RR", 1.5, 0.6, 1.0),
		reported("HR", -0.5, -1, 1),
		reported("MD", -2, -3, -1),
		// a confidence level out of range, a percentage out of range, a percentage without a total
		withLevel(reported("RR", 0.8, 0.6, 1.0), 40),
		withLevel(reported("RR", 0.8, 0.6, 1.0), 99.95),
		{
			Intervention: &model.Arm{Percent: new(120.0), Total: new(50.0)},
			Control:      &model.Arm{Percent: new(10.0), Total: new(50.0)},
		},
		{Intervention: &model.Arm{Percent: new(30.0)}, Control: &model.Arm{Percent: new(10.0)}},
		// an effect without numbers, arms without totals
		{Outcome: "mortality"},
		{Intervention: &model.Arm{Events: new(3.0)}, Control: &model.Arm{Events: new(2.0)}},
		// the two good trials
		counts(10, 100, 20, 100),
		counts(10, 100, 20, 100),
	}

	p, logs := poolPipeline(t)
	pe := p.poolEffects(poolState(effects...))

	require.NotNil(t, pe)
	assert.Equal(t, 2, pe.K, "only the two good trials are pooled")
	assert.Equal(t, 14, bytes.Count(logs.Bytes(), []byte("effect not pooled")), "one warning per bad effect")
}

func TestPoolEffectsUsesTheTitleWithoutDOI(t *testing.T) {
	p, _ := poolPipeline(t)

	st := poolState(counts(10, 100, 20, 100))
	st.Enriched[0].DOI, st.Critiques[0].DOI = "", ""
	st.Enriched[0].Title = "Untitled trial"

	pe := p.poolEffects(st)
	require.NotNil(t, pe)
	assert.Equal(t, []string{"Untitled trial"}, pe.Studies)
}

const (
	effectBatchReply = `{"critiques":[` +
		`{"doi":"10.1000/a","design_quality":"high","relevance":"direct","result_direction":"null",` +
		`"effect":{"outcome":"mortality","intervention":{"events":10,"total":100},"control":{"events":20,"total":100}}},` +
		`{"doi":"10.1000/b","design_quality":"high","relevance":"direct","result_direction":"null",` +
		`"effect":{"outcome":"mortality","intervention":{"events":12,"total":110},"control":{"events":22,"total":108}}}]}`
	effectSynthesisReply = `{"verdict":"moderate","verdict_rationale":"pooled estimate drives it",` +
		`"effect_direction":"Supports","consensus_status":"broad_agreement","consensus":"leans positive"}`
)

// The whole run: the critic's numbers are pooled in Go before the synthesis,
// the judge gets them, the report shows them next to the effect direction.
func TestRunPoolsEffectsBeforeSynthesis(t *testing.T) {
	p, _, llm := runFakes(t, effectBatchReply, effectSynthesisReply)
	runDir := filepath.Join(t.TempDir(), "run")

	st, dir, err := p.Run(t.Context(), Options{
		DOIFile: doiList(t, listDOIA, listDOIB), RunDir: runDir, TextMode: TextLean,
	})
	require.NoError(t, err)

	require.NotNil(t, st.Pooled)
	assert.Equal(t, 2, st.Pooled.K)
	require.Len(t, llm.prompts, 2)
	assert.Contains(t, llm.prompts[1], `"pooled_estimate"`, "the judge reads the pooled estimate")

	require.NotNil(t, st.Report)
	assert.Equal(t, model.EffectSupports, st.Report.EffectDirection, "the judge's word is normalized")
	assert.Equal(t, st.Pooled, st.Report.PooledEstimate)

	//nolint:gosec // reads a file the run just wrote into the test's temp directory
	report, err := os.ReadFile(filepath.Join(dir, "report.md"))
	require.NoError(t, err)
	assert.Contains(t, string(report), "**Effect direction:** supports the claim")
	assert.Contains(t, string(report), "(random effects, DerSimonian-Laird):** RR 0.5")

	loaded, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, st.Pooled, loaded.Pooled, "the pooled estimate round-trips through the artifact")
}

// Both critique forms ask for the effect object the same way; the synthesis
// prompt documents the new field and the pooled-estimate rule.
func TestPromptsCarryTheEffectInstructions(t *testing.T) {
	for name, prompt := range map[string]string{"single": critiqueSystemPrompt, "batch": batchCritiqueSystemPrompt} {
		assert.Contains(t, prompt, "effect (OPTIONAL object", name)
		assert.Contains(t, prompt, "Copy the numbers verbatim", name)
		assert.Contains(t, prompt, "never swap them", name)
		assert.Contains(t, prompt, `"no test reported" alone is not "unclear"`, name)
		assert.Contains(t, prompt, "ci_level", name)
		assert.Contains(t, prompt, "Time-to-event outcomes", name)
		assert.Contains(t, prompt, "Cluster-randomized trials", name)
		assert.Contains(t, prompt, "{percent, total}", name)
		assert.Contains(t, prompt, "reversed: true", name)
		assert.Contains(t, prompt, "never invert the value or the interval yourself", name)
	}

	assert.Contains(t, synthesisSystemPrompt, `effect_direction: "supports" | "no_effect" | "opposite" | "unclear"`)
	assert.Contains(t, synthesisSystemPrompt, "When pooled_estimate is present")
	assert.Contains(t, synthesisSystemPrompt, "never recompute it")
}

func TestSynthesisInputCarriesThePooledEstimate(t *testing.T) {
	p, _ := poolPipeline(t)

	st := poolState(counts(10, 100, 20, 100), counts(10, 100, 20, 100))

	without, err := synthesisUserPrompt(st)
	require.NoError(t, err)
	assert.NotContains(t, without, "pooled_estimate")

	st.Pooled = p.poolEffects(st)

	with, err := synthesisUserPrompt(st)
	require.NoError(t, err)
	assert.Contains(t, with, `"pooled_estimate":{"method":"random_effects_dl","measure":"RR"`)
}

// A missing or unknown effect_direction becomes "unclear", with a warning.
func TestSynthesisNormalizesTheEffectDirection(t *testing.T) {
	p, logs := poolPipeline(t)

	assert.Equal(t, model.EffectNoEffect, p.effectDirection(" No_Effect "))
	assert.Empty(t, logs.String())

	assert.Equal(t, model.EffectUnclear, p.effectDirection("probably yes"))
	assert.Equal(t, model.EffectUnclear, p.effectDirection(""))
	assert.Equal(t, 2, bytes.Count(logs.Bytes(), []byte("replaced with unclear")))
}
