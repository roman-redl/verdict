package pipeline

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/meta"
	"github.com/roman-redl/verdict/internal/model"
)

// seArms is the continuous outcome of means() (10 against 8, 20 per arm) with
// the SD given as the SE of each arm's mean (SD 2: SE = 2 / sqrt(20)).
func seArms(unit string) *model.Effect {
	se := 2 / math.Sqrt(20)

	return &model.Effect{
		Unit:         unit,
		Intervention: &model.Arm{Mean: new(10.0), N: new(20.0), SE: new(se)},
		Control:      &model.Arm{Mean: new(8.0), N: new(20.0), SE: new(se)},
	}
}

// ciArms is the same outcome with the SD given as the 95% interval of each
// arm's mean; a small arm gets the t interval.
func ciArms(t *testing.T, unit string, n float64) *model.Effect {
	t.Helper()

	mult := 1.96

	if n < 60 {
		q, err := meta.TQuantile975(n - 1)
		require.NoError(t, err)

		mult = q
	}

	half := mult * 2 / math.Sqrt(n)

	return &model.Effect{
		Unit:         unit,
		Intervention: &model.Arm{Mean: new(10.0), N: new(n), CILow: new(10 - half), CIHigh: new(10 + half)},
		Control:      &model.Arm{Mean: new(8.0), N: new(n), CILow: new(8 - half), CIHigh: new(8 + half)},
	}
}

// An SD the paper gives as the SE of the arm's mean is restored (SD = SE x
// sqrt(n)): the study pools as if it had stated the SD.
func TestPoolRestoresSDFromTheArmSE(t *testing.T) {
	p, logs := poolPipeline(t)

	got := p.poolEffects(poolState(seArms(unitMmHg)))
	want := p.poolEffects(poolState(means(2, unitMmHg)))

	require.NotNil(t, got)
	require.NotNil(t, want)
	assert.Equal(t, model.MeasureMD, got.Measure)
	assert.InDelta(t, want.Estimate, got.Estimate, 1e-9)
	assert.InDelta(t, want.CILow, got.CILow, 1e-9)
	assert.InDelta(t, want.CIHigh, got.CIHigh, 1e-9)
	require.NotNil(t, got.SD, "the SD is known, so the imprecision band can be taken")
	assert.InDelta(t, 2, *got.SD, 1e-9)

	assert.Contains(t, logs.String(), `msg="arm SD derived"`)
	assert.Contains(t, logs.String(), `from="SE of the arm mean"`)
	assert.Equal(t, 2, strings.Count(logs.String(), `msg="arm SD derived"`), "one line per derived SD")
}

// The 95% interval of the arm's mean gives the SD with t for a small arm and
// 1.96 for a large one.
func TestPoolRestoresSDFromTheArmCI(t *testing.T) {
	for _, n := range []float64{20, 59, 60, 100} {
		p, logs := poolPipeline(t)

		ef := ciArms(t, unitMmHg, n)
		got := p.poolEffects(poolState(ef))

		require.NotNil(t, got, n)
		require.NotNil(t, got.SD, n)
		assert.InDelta(t, 2, *got.SD, 1e-9, "n=%v", n)
		assert.Contains(t, logs.String(), `from="95% CI of the arm mean"`)
	}
}

func TestArmSD(t *testing.T) {
	// 100 +- 1.96 x 5 / sqrt(100): the interval of a mean of 100 with SD 5
	large := &model.Arm{Mean: new(10.0), N: new(100.0), CILow: new(9.02), CIHigh: new(10.98)}
	t20, err := meta.TQuantile975(19)
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		arm      *model.Arm
		wantSD   float64
		wantFrom string
		wantErr  bool
	}{
		{
			name: "the SE of the arm mean",
			arm:  &model.Arm{Mean: new(10.0), N: new(25.0), SE: new(0.5)}, wantSD: 2.5, wantFrom: sdFromSE,
		},
		{name: "a large arm: 1.96", arm: large, wantSD: 5, wantFrom: sdFromCI},
		{
			name: "a small arm: t", wantFrom: sdFromCI,
			arm:    &model.Arm{Mean: new(10.0), N: new(20.0), CILow: new(9.0), CIHigh: new(11.0)},
			wantSD: math.Sqrt(20) * 2 / (2 * t20),
		},
		{
			name: "the SE comes first when both are given", wantFrom: sdFromSE,
			arm:    &model.Arm{Mean: new(10.0), N: new(25.0), SE: new(0.5), CILow: new(1.0), CIHigh: new(50.0)},
			wantSD: 2.5,
		},
		{name: "neither the SE nor the interval", arm: &model.Arm{Mean: new(10.0), N: new(25.0)}},
		{name: "half an interval is nothing", arm: &model.Arm{Mean: new(10.0), N: new(25.0), CILow: new(9.0)}},
		{
			name: "an SE that cannot be one", wantFrom: sdFromSE, wantErr: true,
			arm: &model.Arm{Mean: new(10.0), N: new(25.0), SE: new(-1.0)},
		},
		{
			name: "an empty interval", wantFrom: sdFromCI, wantErr: true,
			arm: &model.Arm{Mean: new(10.0), N: new(25.0), CILow: new(9.0), CIHigh: new(9.0)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, from, err := armSD(tc.arm)

			assert.Equal(t, tc.wantFrom, from)

			if tc.wantErr {
				require.ErrorIs(t, err, meta.ErrInvalid)

				return
			}

			require.NoError(t, err)
			assert.InDelta(t, tc.wantSD, sd, 1e-6)
		})
	}
}

// An arm that states its SD keeps it: the SE is not read.
func TestRestoreSDsKeepsAStatedSD(t *testing.T) {
	p, logs := poolPipeline(t)

	ef := means(2, unitMmHg)
	ef.Intervention.SE = new(99.0)

	assert.Same(t, ef, p.restoreSDs("10.1/x", ef), "nothing to restore: the same effect comes back")
	assert.NotContains(t, logs.String(), "arm SD derived")
}

// The effect that comes in is not modified: a copy carries the restored SD.
func TestRestoreSDsDoesNotModifyTheInput(t *testing.T) {
	p, _ := poolPipeline(t)

	ef := seArms(unitMmHg)
	out := p.restoreSDs("10.1/x", ef)

	assert.NotSame(t, ef, out)
	assert.Nil(t, ef.Intervention.SD)
	require.NotNil(t, out.Intervention.SD)
	assert.InDelta(t, 2, *out.Intervention.SD, 1e-9)
	assert.InDelta(t, 2, *out.Control.SD, 1e-9)
}

// An unusable statistic is not turned into an SD: the arm stays without one
// (logged), and the study drops out as before.
func TestRestoreSDsRefusesAnUnusableStatistic(t *testing.T) {
	p, logs := poolPipeline(t)

	ef := seArms(unitMmHg)
	ef.Control.SE = new(0.0)

	assert.Nil(t, p.poolEffects(poolState(ef)))
	assert.Contains(t, logs.String(), "arm SD not restored")
}

// The SE or the interval of the DIFFERENCE between the arms is never an arm's
// SD (Handbook 6.5.2.2): arms with means but no SD, and a reported MD with its
// interval, pool from the reported estimate alone; without one there is nothing.
func TestPoolNeverDerivesAnArmSDFromTheDifference(t *testing.T) {
	p, logs := poolPipeline(t)

	arms := func() *model.Effect {
		return &model.Effect{
			Unit:         unitMmHg,
			Intervention: &model.Arm{Mean: new(10.0), N: new(20.0)},
			Control:      &model.Arm{Mean: new(8.0), N: new(20.0)},
		}
	}

	ef := arms()
	ef.Measure, ef.Value, ef.CILow, ef.CIHigh = "MD", new(2.0), new(0.5), new(3.5)

	got := p.poolEffects(poolState(ef))
	require.NotNil(t, got)
	assert.Equal(t, model.MeasureMD, got.Measure)
	assert.InDelta(t, 2, got.Estimate, 1e-9)
	assert.Nil(t, got.SD, "a reported estimate carries no SD")

	want, err := meta.DifferenceCI(2, 0.5, 3.5, meta.DefaultLevel)
	require.NoError(t, err)
	assert.InDelta(t, want.Y+1.959964*want.SE, got.CIHigh, 1e-4, "the interval is the one reported")

	assert.Nil(t, p.poolEffects(poolState(arms())), "no SD and no reported estimate: no usable numbers")
	assert.NotContains(t, logs.String(), "arm SD derived")
}

// The SD is restored from the n the paper reports, before a cluster trial's n
// is divided by the design effect.
func TestPoolRestoresSDBeforeTheClusterAdjustment(t *testing.T) {
	p, _ := poolPipeline(t)

	pe := p.poolEffects(poolCorpus(clusterTrial(seArms(unitPoints), &model.Cluster{Clusters: new(4.0), ICC: new(0.05)})))

	require.NotNil(t, pe)
	assert.Equal(t, 28, pe.Participants)
	require.NotNil(t, pe.SD)
	assert.InDelta(t, 2, *pe.SD, 1e-9)
}

// No SD is imputed from another study: a study without any statistic of its
// own stays out of the pool while the others pool.
func TestPoolDoesNotImputeAnSD(t *testing.T) {
	p, _ := poolPipeline(t)

	noSD := &model.Effect{
		Unit:         unitMmHg,
		Intervention: &model.Arm{Mean: new(12.0), N: new(30.0)},
		Control:      &model.Arm{Mean: new(9.0), N: new(30.0)},
	}

	assert.Nil(t, p.poolEffects(poolState(noSD)))

	pe := p.poolEffects(poolState(means(2, unitMmHg), noSD, shifted(means(3, unitMmHg))))
	require.NotNil(t, pe)
	assert.Equal(t, []string{study1, study3}, pe.Studies)
}
