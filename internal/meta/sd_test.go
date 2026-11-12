package meta

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The quantiles are the textbook values of Student's t at 97.5%.
func TestTQuantile975(t *testing.T) {
	for df, want := range map[float64]float64{
		1: 12.706204736, 2: 4.302652730, 5: 2.570581836, 10: 2.228138852, 24: 2.063898562,
		29: 2.045229642, 30: 2.042272456, 58: 2.001717484, 59: 2.000995378, 120: 1.979930405,
	} {
		got, err := TQuantile975(df)
		require.NoError(t, err, df)
		assert.InDelta(t, want, got, 1e-6, "df %v", df)
	}

	// a large sample approaches the normal quantile
	big, err := TQuantile975(1e6)
	require.NoError(t, err)
	assert.InDelta(t, 1.959964, big, 1e-4)
}

func TestTQuantile975Invalid(t *testing.T) {
	for _, df := range []float64{0, 0.5, -3, math.NaN(), math.Inf(1)} {
		_, err := TQuantile975(df)
		require.ErrorIs(t, err, ErrInvalid, df)
	}
}

func TestSDFromSE(t *testing.T) {
	sd, err := SDFromSE(0.5, 25)
	require.NoError(t, err)
	assert.InDelta(t, 2.5, sd, 1e-12)

	for name, in := range map[string][2]float64{
		"zero se": {0, 25}, "negative se": {-1, 25}, "one participant": {0.5, 1}, "no participants": {0.5, 0},
		"nan se": {math.NaN(), 25}, "infinite n": {0.5, math.Inf(1)},
	} {
		_, err := SDFromSE(in[0], in[1])
		require.ErrorIs(t, err, ErrInvalid, name)
	}
}

// Below 60 participants the interval is a t interval, from 60 on a normal one
// (Handbook 6.5.2.3).
func TestSDFromCI(t *testing.T) {
	for _, tc := range []struct {
		name         string
		low, high, n float64
		mult         float64 // the multiplier the Handbook prescribes for this n
	}{
		{name: "small arm uses t with n-1 df", low: 3, high: 5, n: 25, mult: 2.063898562},
		{name: "two participants use t with 1 df", low: -10, high: 30, n: 2, mult: 12.706204736},
		{name: "59 participants still use t", low: 3, high: 5, n: 59, mult: 2.001717484},
		{name: "60 participants use 1.96", low: 3, high: 5, n: 60, mult: 1.96},
		{name: "a large arm uses 1.96", low: 4.2, high: 5.8, n: 400, mult: 1.96},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, err := SDFromCI(tc.low, tc.high, tc.n)
			require.NoError(t, err)
			assert.InDelta(t, math.Sqrt(tc.n)*(tc.high-tc.low)/(2*tc.mult), sd, 1e-6)
		})
	}
}

func TestSDFromCIInvalid(t *testing.T) {
	for name, in := range map[string][3]float64{
		"empty interval": {5, 5, 30}, "reversed interval": {6, 5, 30}, "one participant": {3, 5, 1},
		"no participants": {3, 5, 0}, "nan bound": {math.NaN(), 5, 30}, "infinite bound": {3, math.Inf(1), 30},
	} {
		_, err := SDFromCI(in[0], in[1], in[2])
		require.ErrorIs(t, err, ErrInvalid, name)
	}
}
