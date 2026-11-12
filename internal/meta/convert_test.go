package meta

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ln(OR) of 1.814 is an SMD of 1 (Handbook 10.6: pi / sqrt(3) = 1.814).
func TestSMDFromLogOR(t *testing.T) {
	smd := SMDFromLogOR(Study{Y: math.Pi / math.Sqrt(3), SE: 0.3})
	assert.InDelta(t, 1, smd.Y, 1e-12)
	assert.InDelta(t, 0.3*math.Sqrt(3)/math.Pi, smd.SE, 1e-12)

	or, err := LogOR(10, 100, 20, 100)
	require.NoError(t, err)
	assert.InDelta(t, or.Y*0.5513288954, SMDFromLogOR(or).Y, 1e-9)
}

func TestStudyReversed(t *testing.T) {
	r := Study{Y: 0.4, SE: 0.1}.Reversed()
	assert.InDelta(t, -0.4, r.Y, 0)
	assert.InDelta(t, 0.1, r.SE, 0)
}
