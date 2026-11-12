package pipeline

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// Both critique forms ask for the direction of the outcome and for the arm's
// own SE or interval; the synthesis prompt tells how a converted pool is read.
func TestPromptsAskForTheOrientationFields(t *testing.T) {
	for _, prompt := range []string{critiqueSystemPrompt, batchCritiqueSystemPrompt} {
		assert.Contains(t, prompt, "event_is_harm (true when the counted event is a bad outcome")
		assert.Contains(t, prompt, "higher_is_better (true when a higher value is the better state")
		assert.Contains(t, prompt, `metric ("final"`)
		assert.Contains(t, prompt, "give se instead of sd")
		assert.Contains(t, prompt, "ci_low and ci_high of the arm's mean")
		assert.Contains(t, prompt, "never the SE or the interval of the difference between the arms")
		assert.Contains(t, prompt, "omit when unclear")
	}

	assert.Contains(t, synthesisSystemPrompt, "pooled_estimate.converted is true")
	assert.Contains(t, synthesisSystemPrompt,
		`claim_change is "higher" when the claim says the intervention helps`)
	assert.Contains(t, synthesisSystemPrompt, "do not infer the sign of the original scales")
}

// A metric outside the vocabulary is dropped with a warning (it reads as
// unknown); a valid one is kept in its normal form.
func TestNormalizeEffectMetric(t *testing.T) {
	for _, tc := range []struct {
		in      model.ScoreMetric
		want    model.ScoreMetric
		wantLog bool
	}{
		{in: "final", want: model.MetricFinal},
		{in: " Change ", want: model.MetricChange},
		{in: "", want: ""},
		{in: "final scores", want: "", wantLog: true},
	} {
		t.Run(string(tc.in), func(t *testing.T) {
			p, logs := poolPipeline(t)
			ef := &model.Effect{Metric: tc.in}

			normalizeEffect(ef, doiA, p.log)

			assert.Equal(t, tc.want, ef.Metric)
			assert.Equal(t, tc.wantLog, strings.Contains(logs.String(), "unknown score metric"))
		})
	}
}

// The synthesis payload drops the arm's statistics together with the rest of a
// continuous summary that cannot be real (an n of zero).
func TestUsableEffectVoidsArmStatisticsOfAnEmptyArm(t *testing.T) {
	ef := &model.Effect{
		Intervention: &model.Arm{Mean: new(5.0), N: new(0.0), SE: new(0.4), CILow: new(4.0), CIHigh: new(6.0)},
		Control:      &model.Arm{Mean: new(4.0), N: new(30.0), SE: new(0.4)},
	}

	out := usableEffect(ef)

	require.NotNil(t, out)
	assert.Nil(t, out.Intervention, "nothing is left of an arm of zero participants")
	require.NotNil(t, out.Control)
	assert.InDelta(t, 0.4, *out.Control.SE, 0)
}
