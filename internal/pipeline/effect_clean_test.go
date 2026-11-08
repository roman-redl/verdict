package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

func TestUsableEffect(t *testing.T) {
	tests := []struct {
		name string
		in   *model.Effect
		want *model.Effect
	}{
		{name: "nil in, nil out"},
		{
			name: "valid effect passes through unchanged",
			in: &model.Effect{
				Outcome: "o", Timepoint: "12w", Unit: "mmHg", Reversed: true,
				Intervention: &model.Arm{Events: new(10.0), Total: new(100.0)},
				Control:      &model.Arm{Mean: new(5.0), SD: new(1.5), N: new(40.0)},
				Measure:      "MD", Value: new(-2.0), CILow: new(-3.0), CIHigh: new(-1.0), CILevel: new(95.0),
			},
			want: &model.Effect{
				Outcome: "o", Timepoint: "12w", Unit: "mmHg", Reversed: true,
				Intervention: &model.Arm{Events: new(10.0), Total: new(100.0)},
				Control:      &model.Arm{Mean: new(5.0), SD: new(1.5), N: new(40.0)},
				Measure:      "MD", Value: new(-2.0), CILow: new(-3.0), CIHigh: new(-1.0), CILevel: new(95.0),
			},
		},
		{
			name: "zero-width interval is dropped, the estimate stays",
			in: &model.Effect{
				Outcome: "o", Unit: "kg", Measure: "MD", Value: new(0.02),
				CILow: new(0.0), CIHigh: new(0.0), CILevel: new(95.0), Reversed: true,
			},
			want: &model.Effect{
				Outcome: "o", Unit: "kg", Measure: "MD", Value: new(0.02), Reversed: true,
			},
		},
		{
			name: "value outside the interval drops the interval",
			in: &model.Effect{
				Measure: "RR", Value: new(2.0), CILow: new(0.5), CIHigh: new(1.5), CILevel: new(90.0),
			},
			want: &model.Effect{Measure: "RR", Value: new(2.0)},
		},
		{
			name: "swapped bounds around the value are fine",
			in:   &model.Effect{Measure: "RR", Value: new(1.0), CILow: new(1.5), CIHigh: new(0.5)},
			want: &model.Effect{Measure: "RR", Value: new(1.0), CILow: new(1.5), CIHigh: new(0.5)},
		},
		{
			name: "a lone bound is not judged",
			in:   &model.Effect{Measure: "RR", Value: new(1.0), CILow: new(0.0)},
			want: &model.Effect{Measure: "RR", Value: new(1.0), CILow: new(0.0)},
		},
		{
			name: "interval without a value is judged by width only",
			in: &model.Effect{
				Measure: "RR", CILow: new(0.5), CIHigh: new(1.5),
				Intervention: &model.Arm{Events: new(1.0), Total: new(10.0)},
			},
			want: &model.Effect{
				Measure: "RR", CILow: new(0.5), CIHigh: new(1.5),
				Intervention: &model.Arm{Events: new(1.0), Total: new(10.0)},
			},
		},
		{
			name: "arms of 0 events out of 0 and no value leave nothing",
			in: &model.Effect{
				Outcome: "o", Timepoint: "1y",
				Intervention: &model.Arm{Events: new(0.0), Total: new(0.0)},
				Control:      &model.Arm{Events: new(0.0), Total: new(0.0)},
			},
		},
		{
			name: "negative total voids events and percent",
			in: &model.Effect{
				Outcome:      "o",
				Intervention: &model.Arm{Percent: new(12.0), Total: new(-1.0)},
				Control:      &model.Arm{Events: new(3.0), Total: new(50.0)},
			},
			want: &model.Effect{
				Outcome: "o",
				Control: &model.Arm{Events: new(3.0), Total: new(50.0)},
			},
		},
		{
			name: "zero events with a real total is data",
			in: &model.Effect{
				Intervention: &model.Arm{Events: new(0.0), Total: new(50.0)},
				Control:      &model.Arm{Events: new(2.0), Total: new(50.0)},
			},
			want: &model.Effect{
				Intervention: &model.Arm{Events: new(0.0), Total: new(50.0)},
				Control:      &model.Arm{Events: new(2.0), Total: new(50.0)},
			},
		},
		{
			name: "n of zero voids mean, sd and n but keeps the counts",
			in: &model.Effect{
				Intervention: &model.Arm{Mean: new(1.0), SD: new(2.0), N: new(0.0), Events: new(4.0), Total: new(20.0)},
			},
			want: &model.Effect{
				Intervention: &model.Arm{Events: new(4.0), Total: new(20.0)},
			},
		},
		{
			name: "negative sd voids mean, sd and n",
			in: &model.Effect{
				Measure: "SMD", Value: new(0.3),
				Intervention: &model.Arm{Mean: new(1.0), SD: new(-2.0), N: new(30.0)},
				Control:      &model.Arm{Mean: new(1.0), SD: new(2.0), N: new(30.0)},
			},
			want: &model.Effect{
				Measure: "SMD", Value: new(0.3),
				Control: &model.Arm{Mean: new(1.0), SD: new(2.0), N: new(30.0)},
			},
		},
		{
			name: "a value alone keeps the effect even when both arms are void",
			in: &model.Effect{
				Outcome: "o", Unit: "u", Measure: "HR", Value: new(0.8),
				Intervention: &model.Arm{Events: new(0.0), Total: new(0.0)},
				Control:      &model.Arm{Events: new(0.0), Total: new(0.0)},
			},
			want: &model.Effect{Outcome: "o", Unit: "u", Measure: "HR", Value: new(0.8)},
		},
		{
			name: "interval without value or arms leaves nothing",
			in:   &model.Effect{Outcome: "o", Measure: "RR", CILow: new(0.5), CIHigh: new(1.5)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := usableEffect(tt.in)

			assert.Equal(t, tt.want, got)
		})
	}
}

// The cleaned copy must not write through into the stored critique.
func TestUsableEffectDoesNotMutateTheOriginal(t *testing.T) {
	in := &model.Effect{
		Measure: "MD", Value: new(0.02), CILow: new(0.0), CIHigh: new(0.0), CILevel: new(95.0),
		Intervention: &model.Arm{Events: new(0.0), Total: new(0.0), Mean: new(1.0), SD: new(1.0), N: new(5.0)},
		Control:      &model.Arm{Events: new(0.0), Total: new(0.0)},
	}
	before := *in
	ivBefore, ctBefore := *in.Intervention, *in.Control

	got := usableEffect(in)
	require.NotNil(t, got)

	assert.Equal(t, before, *in, "the effect itself")
	assert.Equal(t, ivBefore, *in.Intervention, "the intervention arm")
	assert.Equal(t, ctBefore, *in.Control, "the control arm")
	assert.NotSame(t, in.Intervention, got.Intervention)
	assert.Nil(t, got.Control)
	assert.Nil(t, got.CILow)
	assert.Equal(t, &model.Arm{Mean: new(1.0), SD: new(1.0), N: new(5.0)}, got.Intervention)
}

// A degenerate interval the critic invented must not reach the synthesis, while
// the stored critique (and so the pooling) keeps its own copy.
func TestSynthesisInputDropsDegenerateInterval(t *testing.T) {
	st := poolState(&model.Effect{
		Outcome: "weight", Measure: "MD", Value: new(0.02), CILow: new(0.0), CIHigh: new(0.0),
		Intervention: &model.Arm{Events: new(0.0), Total: new(0.0)},
		Control:      &model.Arm{Events: new(0.0), Total: new(0.0)},
	})

	out, err := synthesisUserPrompt(st)
	require.NoError(t, err)

	assert.Contains(t, out, `"value":0.02`)
	assert.NotContains(t, out, "ci_low")
	assert.NotContains(t, out, "ci_high")
	assert.NotContains(t, out, `"total"`)
	assert.NotContains(t, out, `"events"`)

	require.NotNil(t, st.Critiques[0].Effect.CILow, "the stored critique stays as the critic wrote it")
}
