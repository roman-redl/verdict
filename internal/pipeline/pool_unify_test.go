package pipeline

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/meta"
	"github.com/roman-redl/verdict/internal/model"
)

// smdPerLogOR is sqrt(3) / pi, the factor from a log OR to an SMD.
const smdPerLogOR = 0.5513288954217921

// leftMetricMixS1 and leftS3MD are left-out entries the tests name more than once.
const (
	leftMetricMixS1 = study1 + " (change vs final scores)"
	leftS3MD        = study3 + " (MD)"
	leftS3HR        = study3 + " (HR)"
	leftS3OR        = study3 + " (OR)"
)

// meansSized is the continuous outcome of means() (in points) with n
// participants per arm.
func meansSized(n, sd float64) *model.Effect {
	return &model.Effect{
		Unit:         unitPoints,
		Intervention: &model.Arm{Mean: new(10.0), SD: new(sd), N: new(n)},
		Control:      &model.Arm{Mean: new(8.0), SD: new(sd), N: new(n)},
	}
}

// withHigherBetter says whether a higher value of the scale is the better state.
func withHigherBetter(ef *model.Effect, better bool) *model.Effect {
	ef.HigherIsBetter = new(better)

	return ef
}

// withEventHarm says whether the counted event is a bad outcome.
func withEventHarm(ef *model.Effect, harm bool) *model.Effect {
	ef.EventIsHarm = new(harm)

	return ef
}

func withMetric(ef *model.Effect, m model.ScoreMetric) *model.Effect {
	ef.Metric = m

	return ef
}

// sized sets the size of study number i (from 0) of a pool state, the sample
// size an effect without arm numbers is weighed by.
func sized(st *State, i, n int) *State {
	st.Enriched[i].SampleSize = n

	return st
}

func signOf(positive bool) float64 {
	if positive {
		return 1
	}

	return -1
}

// A continuous and a binary study of one outcome are put on one scale, and each
// is oriented so that positive favours the intervention: the continuous one by
// higher_is_better, the binary one by event_is_harm. All four sign combinations.
func TestUnifiedPoolOrientsEveryStudy(t *testing.T) {
	g, err := meta.HedgesG(meta.Arm{Mean: 10, SD: 2, N: 20}, meta.Arm{Mean: 8, SD: 2, N: 20})
	require.NoError(t, err)

	lor, err := meta.LogOR(10, 100, 20, 100)
	require.NoError(t, err)

	for _, tc := range []struct {
		name string

		higherIsBetter, eventIsHarm bool
	}{
		{name: "higher is better, the event is a harm", higherIsBetter: true, eventIsHarm: true},
		{name: "higher is better, the event is good", higherIsBetter: true, eventIsHarm: false},
		{name: "higher is worse, the event is a harm", higherIsBetter: false, eventIsHarm: true},
		{name: "higher is worse, the event is good", higherIsBetter: false, eventIsHarm: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, logs := poolPipeline(t)

			// the continuous arm is higher by 2 points; the intervention has half the events
			res := p.pool(poolState(
				withHigherBetter(means(2, unitPoints), tc.higherIsBetter),
				withEventHarm(counts(10, 100, 20, 100), tc.eventIsHarm),
			))
			pe := res.est

			want, err := meta.Pool([]meta.Study{
				{Y: signOf(tc.higherIsBetter) * g.Y, SE: g.SE},
				{Y: signOf(!tc.eventIsHarm) * lor.Y * smdPerLogOR, SE: lor.SE * smdPerLogOR},
			})
			require.NoError(t, err)

			require.NotNil(t, pe)
			assert.Equal(t, model.MeasureSMD, pe.Measure)
			assert.True(t, pe.Converted)
			assert.Equal(t, 2, pe.K)
			assert.Equal(t, []string{study1, study2}, pe.Studies)
			assert.Equal(t, 240, pe.Participants)
			assert.Empty(t, pe.LeftOut)
			assert.Empty(t, res.leftOut)
			assert.InDelta(t, want.Estimate, pe.Estimate, 1e-9)
			assert.InDelta(t, want.CILow, pe.CILow, 1e-9)
			assert.InDelta(t, want.CIHigh, pe.CIHigh, 1e-9)
			assert.Equal(t, "outcome (standardized, positive favours the intervention)", pe.Outcome)
			assert.Contains(t, logs.String(), "pooled as a standardized mean difference")
		})
	}
}

// The studies of the unified pool in the four sign combinations point the same
// way when both favour the intervention: positive.
func TestUnifiedPoolBothFavouringTheInterventionIsPositive(t *testing.T) {
	p, _ := poolPipeline(t)

	// fewer harmful events and a lower pain score: both favour the intervention
	pe := p.poolEffects(poolState(
		withHigherBetter(&model.Effect{
			Intervention: &model.Arm{Mean: new(3.0), SD: new(2.0), N: new(20.0)},
			Control:      &model.Arm{Mean: new(5.0), SD: new(2.0), N: new(20.0)},
		}, false),
		withEventHarm(counts(10, 100, 20, 100), true),
	))

	require.NotNil(t, pe)
	assert.Greater(t, pe.Estimate, 0.0)
	assert.Greater(t, pe.CILow, 0.0)
}

// A study whose direction is unknown cannot join the unified pool; the reason
// is its own, not "other measure".
func TestUnifiedPoolLeavesOutUnknownDirection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		effects []*model.Effect
		want    []string
	}{
		{
			name: "a continuous scale without higher_is_better",
			effects: []*model.Effect{
				withEventHarm(counts(10, 100, 20, 100), true),
				withEventHarm(counts(12, 100, 22, 100), true),
				meansSized(100, 2), // no direction stated
				withHigherBetter(shifted(meansSized(100, 3)), true),
			},
			want: []string{"10.1/s3 (direction of scale unknown)"},
		},
		{
			name: "a binary outcome without event_is_harm",
			effects: []*model.Effect{
				withEventHarm(counts(10, 100, 20, 100), true),
				counts(12, 100, 22, 100), // no polarity stated
				withHigherBetter(meansSized(100, 2), true),
				withHigherBetter(shifted(meansSized(100, 3)), true),
			},
			want: []string{"10.1/s2 (direction of scale unknown)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, logs := poolPipeline(t)

			res := p.pool(poolState(tc.effects...))

			require.NotNil(t, res.est)
			assert.Equal(t, model.MeasureSMD, res.est.Measure)
			assert.True(t, res.est.Converted)
			assert.Equal(t, 3, res.est.K)
			assert.Equal(t, tc.want, res.est.LeftOut)
			assert.Contains(t, logs.String(), `reason="direction of scale unknown" count=1`)
		})
	}
}

// Final values and change scores are not mixed in one SMD pool: the metric with
// more participants stays, the other is left out; a study that does not state its
// metric, and a binary study, join either.
func TestUnifiedPoolDoesNotMixFinalAndChange(t *testing.T) {
	for _, tc := range []struct {
		name     string
		effects  []*model.Effect
		wantK    int
		wantLeft []string
	}{
		{
			name: "change has more participants: the final scores are left out",
			effects: []*model.Effect{
				withMetric(withHigherBetter(means(2, unitPoints), true), model.MetricFinal),
				withMetric(withHigherBetter(shifted(meansSized(50, 2)), true), model.MetricChange),
				withEventHarm(counts(10, 100, 20, 100), true),
			},
			wantK: 2, wantLeft: []string{leftMetricMixS1},
		},
		{
			name: "final has more participants: the change scores are left out",
			effects: []*model.Effect{
				withMetric(withHigherBetter(meansSized(50, 2), true), model.MetricFinal),
				withMetric(withHigherBetter(shifted(means(2, unitPoints)), true), model.MetricChange),
				withEventHarm(counts(10, 100, 20, 100), true),
			},
			wantK: 2, wantLeft: []string{"10.1/s2 (change vs final scores)"},
		},
		{
			name: "a tie keeps the final scores",
			effects: []*model.Effect{
				withMetric(withHigherBetter(means(2, unitPoints), true), model.MetricChange),
				withMetric(withHigherBetter(shifted(means(2, unitPoints)), true), model.MetricFinal),
				withEventHarm(counts(10, 100, 20, 100), true),
			},
			wantK: 2, wantLeft: []string{leftMetricMixS1},
		},
		{
			name: "an unstated metric and a binary study take no side",
			effects: []*model.Effect{
				withMetric(withHigherBetter(means(2, unitPoints), true), model.MetricFinal),
				withMetric(withHigherBetter(shifted(meansSized(50, 2)), true), model.MetricChange),
				withHigherBetter(shifted(shifted(means(3, unitPoints))), true), // metric unknown
				withEventHarm(counts(10, 100, 20, 100), true),
			},
			wantK: 3, wantLeft: []string{leftMetricMixS1},
		},
		{
			name: "one metric only: nothing is left out",
			effects: []*model.Effect{
				withMetric(withHigherBetter(means(2, unitPoints), true), model.MetricChange),
				withMetric(withHigherBetter(shifted(means(2, unitPoints)), true), model.MetricChange),
				withEventHarm(counts(10, 100, 20, 100), true),
			},
			wantK: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			pe := p.poolEffects(poolState(tc.effects...))

			require.NotNil(t, pe)
			assert.Equal(t, model.MeasureSMD, pe.Measure)
			assert.True(t, pe.Converted)
			assert.Equal(t, tc.wantK, pe.K)
			assert.Equal(t, tc.wantLeft, pe.LeftOut)
		})
	}
}

// The unified pool replaces the best native pool only when it holds MORE
// participants and at least two studies; otherwise the pool is the native one,
// as before, and the others are left out for their measure.
func TestUnifiedPoolIsChosenOnlyWithMoreParticipants(t *testing.T) {
	for _, tc := range []struct {
		name     string
		state    func() *State
		measure  model.Measure
		wantK    int
		wantLeft []string
	}{
		{
			name: "fewer participants than the native pool",
			state: func() *State {
				// a reported RR of 2000 participants and the counts make the RR pool (2200);
				// the counts and the means would be 240
				return sized(poolState(
					reported("RR", 0.7, 0.5, 0.98),
					withEventHarm(counts(10, 100, 20, 100), true),
					withHigherBetter(means(2, unitPoints), true),
				), 0, 2000)
			},
			measure: model.MeasureRR, wantK: 2, wantLeft: []string{leftS3MD},
		},
		{
			name: "as many participants as the native pool",
			state: func() *State {
				return sized(poolState(
					reported("RR", 0.7, 0.5, 0.98),
					withEventHarm(counts(10, 100, 20, 100), true),
					withHigherBetter(means(2, unitPoints), true),
				), 0, 40)
			},
			measure: model.MeasureRR, wantK: 2, wantLeft: []string{leftS3MD},
		},
		{
			name: "only one study can be put on the scale",
			state: func() *State {
				return poolState(
					withEventHarm(counts(10, 100, 20, 100), true),
					means(2, unitPoints), // direction unknown
					reported("HR", 0.6, 0.4, 0.9),
				)
			},
			measure: model.MeasureRR, wantK: 1, wantLeft: []string{study2 + " (MD)", leftS3HR},
		},
		{
			name: "more participants",
			state: func() *State {
				return sized(poolState(
					reported("RR", 0.7, 0.5, 0.98),
					withEventHarm(counts(10, 100, 20, 100), true),
					withHigherBetter(means(2, unitPoints), true),
				), 0, 30)
			},
			measure: model.MeasureSMD, wantK: 2, wantLeft: []string{"10.1/s1 (RR)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			pe := p.poolEffects(tc.state())

			require.NotNil(t, pe)
			assert.Equal(t, tc.measure, pe.Measure)
			assert.Equal(t, tc.measure == model.MeasureSMD, pe.Converted)
			assert.Equal(t, tc.wantK, pe.K)
			assert.Equal(t, tc.wantLeft, pe.LeftOut)
		})
	}
}

// A reported HR, an RR or OR without counts and an MD without SDs cannot be put
// on the standardized scale: they stay out under their own measure.
func TestUnifiedPoolDoesNotConvertReportedEstimates(t *testing.T) {
	mdOnly := reported("MD", 1.5, 0.5, 2.5)
	mdOnly.Unit = unitPoints

	for _, tc := range []struct {
		name string
		ef   *model.Effect
		want string
	}{
		{name: "a hazard ratio", ef: reported("HR", 0.6, 0.4, 0.9), want: leftS3HR},
		{name: "a risk ratio without counts", ef: reported("RR", 0.6, 0.4, 0.9), want: study3 + " (RR)"},
		{name: "an odds ratio without counts", ef: reported("OR", 0.6, 0.4, 0.9), want: leftS3OR},
		{name: "a mean difference without SDs", ef: mdOnly, want: leftS3MD},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			pe := p.poolEffects(poolState(
				withEventHarm(counts(10, 100, 20, 100), true),
				withHigherBetter(means(2, unitPoints), true),
				tc.ef,
			))

			require.NotNil(t, pe)
			assert.Equal(t, model.MeasureSMD, pe.Measure)
			assert.Equal(t, 2, pe.K)
			assert.Equal(t, []string{tc.want}, pe.LeftOut)
		})
	}
}

// With a single measure among the eligible studies nothing changes, whatever
// the labels say: the result is the one the same studies gave without labels.
func TestUnifiedPoolLeavesSingleMeasurePoolsAlone(t *testing.T) {
	for _, tc := range []struct {
		name    string
		effects func() []*model.Effect
		measure model.Measure
	}{
		{
			name: "counts",
			effects: func() []*model.Effect {
				return []*model.Effect{counts(10, 100, 20, 100), counts(22, 200, 40, 200), counts(5, 60, 9, 60)}
			},
			measure: model.MeasureRR,
		},
		{
			name: "means in one unit",
			effects: func() []*model.Effect {
				return []*model.Effect{means(2, unitMmHg), shifted(means(3, unitMmHg))}
			},
			measure: model.MeasureMD,
		},
		{
			name: "means in different units",
			effects: func() []*model.Effect {
				return []*model.Effect{means(2, unitMmHg), shifted(means(3, unitPoints))}
			},
			measure: model.MeasureSMD,
		},
		{
			name: "reported ratios",
			effects: func() []*model.Effect {
				return []*model.Effect{reported("OR", 0.7, 0.5, 0.98), reported("OR", 0.9, 0.6, 1.3)}
			},
			measure: model.MeasureOR,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			plain := p.pool(poolState(tc.effects()...))

			labelled := tc.effects()
			for _, ef := range labelled {
				withEventHarm(withHigherBetter(withMetric(ef, model.MetricFinal), true), true)
			}

			got := p.pool(poolState(labelled...))

			require.NotNil(t, plain.est)
			assert.Equal(t, tc.measure, plain.est.Measure)
			assert.False(t, plain.est.Converted)
			assert.Equal(t, plain, got, "labels change nothing when one measure covers every study")
		})
	}
}

// Without the new labels (an artifact written before them, or a critic that
// could not tell) a mix of measures behaves as before: the native pool with the
// most participants, the others left out for their measure.
func TestUnifiedPoolNeedsTheLabels(t *testing.T) {
	p, _ := poolPipeline(t)

	res := p.pool(poolState(means(2, unitPoints), counts(10, 100, 20, 100)))

	require.NotNil(t, res.est)
	assert.Equal(t, model.MeasureRR, res.est.Measure)
	assert.False(t, res.est.Converted)
	assert.Equal(t, []string{"10.1/s1 (MD)"}, res.est.LeftOut)
}

// The converted pool reads with the imprecision rule of an SMD and the claim is
// read against the orientation: positive favours the intervention, so a claim
// that the intervention helps is claim_change "higher" and a claim that it harms
// is "lower".
func TestUnifiedPoolDirectionFollowsTheOrientation(t *testing.T) {
	p, _ := poolPipeline(t)

	// both studies favour the intervention strongly: a lower pain score, fewer harmful events
	pe := p.poolEffects(poolState(
		withHigherBetter(&model.Effect{
			Intervention: &model.Arm{Mean: new(3.0), SD: new(2.0), N: new(100.0)},
			Control:      &model.Arm{Mean: new(5.0), SD: new(2.0), N: new(100.0)},
		}, false),
		withEventHarm(counts(5, 100, 25, 100), true),
	))

	require.NotNil(t, pe)
	require.True(t, pe.Converted)
	require.Greater(t, pe.CILow, 0.2, "the whole interval is beyond the trivial band")

	imp := p.assessImprecision(pe)
	require.NotNil(t, imp)
	assert.Equal(t, model.TargetHigher, imp.Target, "a positive standardized effect is 'higher'")

	for _, tc := range []struct {
		claim string
		want  model.EffectDirection
	}{
		{claim: string(model.ClaimHigher), want: model.EffectSupports}, // the claim: the intervention helps
		{claim: string(model.ClaimLower), want: model.EffectOpposite},  // the claim: the intervention harms
	} {
		got, warning := p.directionFromPooled(imp, tc.claim, model.EffectUnclear)
		assert.Equal(t, tc.want, got, tc.claim)
		assert.NotEmpty(t, warning)
	}
}

// A converted pool says so in the report line, next to the orientation.
func TestUnifiedPoolShowsInTheReportLine(t *testing.T) {
	p, _ := poolPipeline(t)

	pe := p.poolEffects(poolState(
		withHigherBetter(means(2, unitPoints), true),
		withEventHarm(counts(10, 100, 20, 100), true),
	))

	require.NotNil(t, pe)

	line := pooledLine(pe)
	assert.Contains(t, line, "measures converted to one standardized scale")
	assert.Contains(t, line, "SMD ")
	assert.Contains(t, line, "positive favours the intervention")
	assert.NotContains(t, pooledLine(&model.PooledEstimate{Method: model.PoolRandomEffects, Measure: model.MeasureSMD}),
		"converted")
}

// The OR scaled by sqrt(3)/pi is the factor of the Handbook (ln OR = 1.814 SMD).
func TestSMDPerLogORConstant(t *testing.T) {
	assert.InDelta(t, smdPerLogOR, math.Sqrt(3)/math.Pi, 1e-15)
}
