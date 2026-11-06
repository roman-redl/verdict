package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEffectDirection(t *testing.T) {
	for in, want := range map[string]EffectDirection{
		"supports":  EffectSupports,
		"No_Effect": EffectNoEffect,
		"OPPOSITE":  EffectOpposite,
		"unclear":   EffectUnclear,
	} {
		got, err := ParseEffectDirection(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)

		got, err = ParseEffectDirection(" " + in + " ")
		require.NoError(t, err, "spaces are ignored")
		assert.Equal(t, want, got, in)
	}

	for _, in := range []string{"", "positive", "no effect"} {
		_, err := ParseEffectDirection(in)
		require.ErrorContains(t, err, "unknown effect direction", in)
	}
}

func TestParseMeasure(t *testing.T) {
	for in, want := range map[string]Measure{
		"RR": MeasureRR, "or": MeasureOR, "Hr": MeasureHR, "md": MeasureMD, "SMD": MeasureSMD,
	} {
		got, err := ParseMeasure(" " + in + " ")
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	_, err := ParseMeasure("RRR")
	require.ErrorContains(t, err, "unknown effect measure")

	assert.True(t, MeasureRR.IsRatio())
	assert.True(t, MeasureOR.IsRatio())
	assert.True(t, MeasureHR.IsRatio())
	assert.False(t, MeasureMD.IsRatio())
	assert.False(t, MeasureSMD.IsRatio())
}

// The critic's JSON loads into Effect; a missing number stays nil, a stated
// zero does not.
func TestEffectJSON(t *testing.T) {
	var c Critique

	require.NoError(t, json.Unmarshal([]byte(`{"doi":"10.1/x","effect":{
		"outcome":"mortality","intervention":{"events":0,"total":50},"control":{"events":5,"total":52}}}`), &c))

	require.NotNil(t, c.Effect)
	assert.Equal(t, "mortality", c.Effect.Outcome)
	require.NotNil(t, c.Effect.Intervention.Events)
	assert.InDelta(t, 0, *c.Effect.Intervention.Events, 0)
	assert.Nil(t, c.Effect.Intervention.Mean)
	assert.Nil(t, c.Effect.Value)

	var none Critique

	require.NoError(t, json.Unmarshal([]byte(`{"doi":"10.1/x"}`), &none))
	assert.Nil(t, none.Effect)
}
