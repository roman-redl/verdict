package meta

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bcg is the BCG vaccine dataset (R package metafor, dat.bcg): tuberculosis
// cases among vaccinated (events/total) and control participants in 13 trials.
// Colditz et al. 1994, JAMA 271:698-702.
var bcg = []struct{ vac, vacTotal, ctl, ctlTotal float64 }{
	{4, 123, 11, 139},
	{6, 306, 29, 303},
	{3, 231, 11, 220},
	{62, 13598, 248, 12867},
	{33, 5069, 47, 5808},
	{180, 1541, 372, 1451},
	{8, 2545, 10, 629},
	{505, 88391, 499, 88391},
	{29, 7499, 45, 7277},
	{17, 1716, 65, 1665},
	{186, 50634, 141, 27338},
	{5, 2498, 3, 2341},
	{27, 16913, 29, 17854},
}

// Expected values of escalc(measure="RR") and rma(yi, vi, method="DL") on that
// data: W. Viechtbauer, "Estimating and Interpreting Heterogeneity and Summary
// Effects", Cochrane Methods Training Event 2016, slides 47 and 50
// (https://www.wvbauer.com/lib/exe/fetch.php/talks:2016_viechtbauer_cochrane_smg_training.pdf):
// per-study yi and vi to four decimals, then
// tau^2 = 0.3088, I^2 = 92.12%, Q(df = 12) = 152.2330, estimate = -0.7141,
// se = 0.1787, ci.lb = -1.0644, ci.ub = -0.3638 (RR 0.49, 95% CI 0.34 to 0.70).
var (
	bcgYi = []float64{
		-0.8893, -1.5854, -1.3481, -1.4416, -0.2175, -0.7861, -1.6209, 0.0120, -0.4694, -1.3713, -0.3394, 0.4459, -0.0173,
	}
	bcgVi = []float64{
		0.3256, 0.1946, 0.4154, 0.0200, 0.0512, 0.0069, 0.2230, 0.0040, 0.0564, 0.0730, 0.0124, 0.5325, 0.0714,
	}
)

func bcgStudies(t *testing.T) []Study {
	t.Helper()

	out := make([]Study, len(bcg))

	for i, b := range bcg {
		s, err := LogRR(b.vac, b.vacTotal, b.ctl, b.ctlTotal)
		require.NoError(t, err)

		out[i] = s
	}

	return out
}

func TestLogRRMatchesMetaforOnBCG(t *testing.T) {
	for i, s := range bcgStudies(t) {
		assert.InDelta(t, bcgYi[i], s.Y, 0.00006, "trial %d: log RR", i+1)
		assert.InDelta(t, bcgVi[i], s.SE*s.SE, 0.00006, "trial %d: variance", i+1)
	}
}

func TestPoolBCGDerSimonianLaird(t *testing.T) {
	res, err := Pool(bcgStudies(t))
	require.NoError(t, err)

	assert.Equal(t, 13, res.K)
	assert.InDelta(t, 0.3088, res.Tau2, 0.0001)
	assert.InDelta(t, 152.2330, res.Q, 0.001)
	assert.InDelta(t, 92.12, res.I2, 0.01)
	assert.InDelta(t, -0.7141, res.Estimate, 0.0001)
	assert.InDelta(t, 0.1787, res.SE, 0.0001)
	assert.InDelta(t, -1.0644, res.CILow, 0.0001)
	assert.InDelta(t, -0.3638, res.CIHigh, 0.0001)
	assert.InDelta(t, 0.49, math.Exp(res.Estimate), 0.005)
	assert.InDelta(t, 0.34, math.Exp(res.CILow), 0.005)
	assert.InDelta(t, 0.70, math.Exp(res.CIHigh), 0.005)
}

// The pool of the published per-study figures reproduces the same summary:
// the check does not depend on LogRR.
func TestPoolFromPublishedEffects(t *testing.T) {
	studies := make([]Study, len(bcgYi))
	for i := range studies {
		studies[i] = Study{Y: bcgYi[i], SE: math.Sqrt(bcgVi[i])}
	}

	res, err := Pool(studies)
	require.NoError(t, err)
	assert.InDelta(t, -0.7141, res.Estimate, 0.002)
	assert.InDelta(t, 0.3088, res.Tau2, 0.002)
}

func TestPoolHomogeneousStudiesHaveNoBetweenVariance(t *testing.T) {
	res, err := Pool([]Study{{Y: 0.5, SE: 0.2}, {Y: 0.5, SE: 0.2}, {Y: 0.5, SE: 0.2}})
	require.NoError(t, err)

	assert.InDelta(t, 0.5, res.Estimate, 1e-12)
	assert.InDelta(t, 0.0, res.Tau2, 1e-12)
	assert.InDelta(t, 0.0, res.I2, 1e-12)
	assert.InDelta(t, 0.2/math.Sqrt(3), res.SE, 1e-12)
}

func TestPoolRejectsBadInput(t *testing.T) {
	_, err := Pool(nil)
	require.ErrorIs(t, err, ErrNoStudies)

	_, err = Pool([]Study{{Y: 1, SE: 0.1}})
	require.ErrorIs(t, err, ErrInvalid, "one study is not a pool")

	_, err = Pool([]Study{{Y: 1, SE: 0.1}, {Y: 1, SE: 0}})
	require.ErrorIs(t, err, ErrInvalid)

	_, err = Pool([]Study{{Y: math.NaN(), SE: 0.1}, {Y: 1, SE: 0.1}})
	require.ErrorIs(t, err, ErrInvalid)
}

func TestSingle(t *testing.T) {
	_, err := Single(Study{Y: 0, SE: 0})
	require.ErrorIs(t, err, ErrInvalid)

	// 10/689 vs 18/897: RR 0.72, 95% CI 0.34 to 1.56
	s, err := LogRR(10, 689, 18, 897)
	require.NoError(t, err)

	res, err := Single(s)
	require.NoError(t, err)

	assert.Equal(t, 1, res.K)
	assert.InDelta(t, 0.72, math.Exp(res.Estimate), 0.005)
	assert.InDelta(t, 0.34, math.Exp(res.CILow), 0.005)
	assert.InDelta(t, 1.56, math.Exp(res.CIHigh), 0.005)
}

func TestLogRRZeroCellGetsContinuityCorrection(t *testing.T) {
	s, err := LogRR(0, 50, 5, 50)
	require.NoError(t, err)

	// 0.5 in every cell: events 0.5/51 vs 5.5/51
	assert.InDelta(t, math.Log(0.5/5.5), s.Y, 1e-12)
	assert.InDelta(t, 2.142602, s.SE*s.SE, 1e-5)
}

func TestLogRRDoubleZeroAndInvalid(t *testing.T) {
	_, err := LogRR(0, 40, 0, 40)
	require.ErrorIs(t, err, ErrNoEvents)

	_, err = LogRR(40, 40, 30, 30)
	require.ErrorIs(t, err, ErrNoEvents)

	for _, c := range [][4]float64{{-1, 10, 2, 10}, {11, 10, 2, 10}, {1, 0, 2, 10}, {1, 10, 2, -5}} {
		_, err = LogRR(c[0], c[1], c[2], c[3])
		require.ErrorIs(t, err, ErrInvalid, "%v", c)
	}
}

func TestMD(t *testing.T) {
	s, err := MD(Arm{Mean: 10, SD: 2, N: 20}, Arm{Mean: 8, SD: 2, N: 20})
	require.NoError(t, err)
	assert.InDelta(t, 2, s.Y, 1e-12)
	assert.InDelta(t, math.Sqrt(0.4), s.SE, 1e-12)

	_, err = MD(Arm{Mean: 10, SD: 0, N: 20}, Arm{Mean: 8, SD: 2, N: 20})
	require.ErrorIs(t, err, ErrInvalid)

	_, err = MD(Arm{Mean: 10, SD: 2, N: 1}, Arm{Mean: 8, SD: 2, N: 20})
	require.ErrorIs(t, err, ErrInvalid)
}

func TestHedgesG(t *testing.T) {
	// pooled SD 2, d = 1, df = 38: J = 1 - 3/151, variance = 0.1 + g^2/80
	s, err := HedgesG(Arm{Mean: 10, SD: 2, N: 20}, Arm{Mean: 8, SD: 2, N: 20})
	require.NoError(t, err)

	g := 1 - 3.0/151
	assert.InDelta(t, g, s.Y, 1e-12)
	assert.InDelta(t, 0.1+g*g/80, s.SE*s.SE, 1e-12)

	_, err = HedgesG(Arm{Mean: 1, SD: -1, N: 20}, Arm{Mean: 8, SD: 2, N: 20})
	require.ErrorIs(t, err, ErrInvalid)
}

func TestRatioCI(t *testing.T) {
	s, err := RatioCI(0.80, 0.65, 0.99)
	require.NoError(t, err)
	assert.InDelta(t, math.Log(0.80), s.Y, 1e-12)
	assert.InDelta(t, (math.Log(0.99)-math.Log(0.65))/3.92, s.SE, 1e-12)

	for _, c := range [][3]float64{{0, 0.5, 1}, {2, 0.5, 1}, {0.5, 1, 0.9}, {0.5, -1, 1}} {
		_, err = RatioCI(c[0], c[1], c[2])
		require.ErrorIs(t, err, ErrInvalid, "%v", c)
	}
}

func TestDifferenceCI(t *testing.T) {
	s, err := DifferenceCI(-0.22, -0.40, -0.03)
	require.NoError(t, err)
	assert.InDelta(t, -0.22, s.Y, 1e-12)
	assert.InDelta(t, 0.37/3.92, s.SE, 1e-12)

	for _, c := range [][3]float64{{0, 1, 2}, {0, -1, -2}, {1, 1, 1}} {
		_, err = DifferenceCI(c[0], c[1], c[2])
		require.ErrorIs(t, err, ErrInvalid, "%v", c)
	}
}
