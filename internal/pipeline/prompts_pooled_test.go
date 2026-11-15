package pipeline

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

func TestJudgePooledStatesI2WithItsUnit(t *testing.T) {
	i2 := 0.9620228790208277
	pe := &model.PooledEstimate{Measure: "RR", Estimate: 0.67, CILow: 0.54, CIHigh: 0.83, K: 9, I2: &i2}

	b, err := json.Marshal(judgePooled(pe))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	assert.Equal(t, "1.0%", got["i2_percent"])
	assert.NotContains(t, got, "i2", "the bare number is what a judge misread as a fraction")
	assert.Equal(t, "RR", got["measure"])
	assert.InDelta(t, 9, got["k"], 0)
	assert.Equal(t, &i2, pe.I2, "the state keeps the number")
}

func TestJudgePooledWithoutI2(t *testing.T) {
	b, err := json.Marshal(judgePooled(&model.PooledEstimate{Measure: "RR", K: 1}))
	require.NoError(t, err)

	assert.NotContains(t, string(b), "i2")
}
