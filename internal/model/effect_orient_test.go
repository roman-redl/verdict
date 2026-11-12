package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseScoreMetric(t *testing.T) {
	for in, want := range map[string]ScoreMetric{"final": MetricFinal, "Change": MetricChange, "FINAL": MetricFinal} {
		got, err := ParseScoreMetric(" " + in + " ")
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	for _, in := range []string{"", "baseline", "final scores"} {
		_, err := ParseScoreMetric(in)
		require.ErrorContains(t, err, "unknown score metric", in)
	}
}

// The new fields are optional: an old artifact has none and reads as unknown,
// a stated false is not the same as a missing value.
func TestEffectJSONOrientationFields(t *testing.T) {
	var c Critique

	require.NoError(t, json.Unmarshal([]byte(`{"doi":"10.1/x","effect":{
		"higher_is_better":false,"metric":"change","event_is_harm":true,
		"intervention":{"mean":5,"n":30,"se":0.4,"ci_low":4.2,"ci_high":5.8}}}`), &c))

	ef := c.Effect
	require.NotNil(t, ef)
	require.NotNil(t, ef.HigherIsBetter)
	assert.False(t, *ef.HigherIsBetter)
	assert.Equal(t, MetricChange, ef.Metric)
	require.NotNil(t, ef.EventIsHarm)
	assert.True(t, *ef.EventIsHarm)
	require.NotNil(t, ef.Intervention.SE)
	assert.InDelta(t, 0.4, *ef.Intervention.SE, 0)
	assert.InDelta(t, 4.2, *ef.Intervention.CILow, 0)
	assert.InDelta(t, 5.8, *ef.Intervention.CIHigh, 0)

	var old Critique

	require.NoError(t, json.Unmarshal([]byte(`{"doi":"10.1/x","effect":{"outcome":"pain"}}`), &old))
	assert.Nil(t, old.Effect.HigherIsBetter)
	assert.Nil(t, old.Effect.EventIsHarm)
	assert.Empty(t, old.Effect.Metric)
}
