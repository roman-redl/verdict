package grade

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ratio(est, low, high float64) Input {
	return Input{Measure: MeasureRatio, Estimate: est, CILow: low, CIHigh: high}
}

func TestAssessImprecision(t *testing.T) {
	cases := []struct {
		name        string
		in          Input
		levels      int
		target      Target
		band        []float64 // low, high; nil = no band
		sizeChecked bool
		reason      string
	}{
		{
			name: "RR includes no effect and crosses the lower threshold", in: ratio(0.77, 0.48, 1.23),
			levels: 1, target: TargetLittleOrNoDifference, band: []float64{0.75, 1.25}, sizeChecked: true,
			reason: "CI 0.48 to 1.23 includes no effect and crosses the 0.75 threshold; " +
				"the estimate 0.77 leans toward a decrease, but the interval includes no effect",
		},
		{
			name: "RR 1.29 includes no effect: no difference seen, the lean is an increase", in: ratio(1.29, 0.99, 1.68),
			levels: 1, target: TargetLittleOrNoDifference, band: []float64{0.75, 1.25}, sizeChecked: true,
			reason: "CI 0.99 to 1.68 includes no effect and crosses the 1.25 threshold; " +
				"the estimate 1.29 leans toward an increase, but the interval includes no effect",
		},
		{
			name: "RR 1.29 with the limits in the wrong order", in: ratio(1.29, 1.68, 0.99),
			levels: 1, target: TargetLittleOrNoDifference, band: []float64{0.75, 1.25}, sizeChecked: true,
			reason: "CI 0.99 to 1.68 includes no effect and crosses the 1.25 threshold; " +
				"the estimate 1.29 leans toward an increase, but the interval includes no effect",
		},
		{
			name: "RR 0.67 with a very wide interval: the evidence cannot tell", in: ratio(0.67, 0.12, 3.84),
			levels: 2, target: TargetUncertain, band: []float64{0.75, 1.25}, sizeChecked: true,
			reason: "CI 0.12 to 3.84 includes no effect and crosses the 0.75 and 1.25 thresholds; " +
				"the estimate 0.67 leans toward a decrease, but the interval includes no effect",
		},
		{
			name: "RR 0.67 very wide, limits in the wrong order", in: ratio(0.67, 3.84, 0.12),
			levels: 2, target: TargetUncertain, band: []float64{0.75, 1.25}, sizeChecked: true,
		},
		{
			name: "RR 0.35 excludes no effect: the direction stays", in: ratio(0.35, 0.18, 0.67),
			levels: 1, target: TargetLower, sizeChecked: true,
		},
		{
			name: "RR 0.35 excludes no effect, limits in the wrong order", in: ratio(0.35, 0.67, 0.18),
			levels: 1, target: TargetLower, sizeChecked: true,
		},
		{
			name: "RR precisely excludes no effect", in: ratio(0.70, 0.55, 0.89),
			levels: 0, target: TargetLower, sizeChecked: true,
			reason: "CI 0.55 to 0.89 excludes no effect and is not wide: the upper limit is 1.62 times the lower (cutoff 3)",
		},
		{
			name: "HR precisely excludes no effect", in: ratio(1.38, 1.18, 1.62),
			levels: 0, target: TargetHigher, sizeChecked: true,
		},
		{
			name: "RR with a ratio of limits just under 3", in: ratio(0.50, 0.35, 0.71),
			levels: 0, target: TargetLower, sizeChecked: true,
		},
		{
			name: "RR with a wide interval behind a large effect", in: ratio(3.6, 1.06, 12.26),
			levels: 1, target: TargetHigher, sizeChecked: true,
			reason: "CI 1.06 to 12.3 excludes no effect but is wide: the upper limit is 11.6 times the lower (cutoff 3)",
		},
		{
			name: "RR near the upper threshold", in: ratio(1.20, 0.996, 1.449),
			levels: 1, target: TargetLittleOrNoDifference, band: []float64{0.75, 1.25}, sizeChecked: true,
			reason: "CI 0.996 to 1.45 includes no effect and crosses the 1.25 threshold; " +
				"the estimate 1.2 leans toward an increase, but the interval includes no effect",
		},
		{
			name: "RR within the thresholds", in: ratio(0.9, 0.8, 1.1),
			levels: 0, target: TargetLittleOrNoDifference, band: []float64{0.75, 1.25}, sizeChecked: true,
			reason: "CI 0.8 to 1.1 includes no effect and lies within the 0.75 to 1.25 thresholds",
		},
		{
			name: "RR reaching past both thresholds", in: ratio(1.0, 0.4, 2.5),
			levels: 2, target: TargetUncertain, band: []float64{0.75, 1.25}, sizeChecked: true,
			reason: "CI 0.4 to 2.5 includes no effect and crosses the 0.75 and 1.25 thresholds",
		},
		{
			name: "estimate above the band: the interval includes no effect, so no direction", in: ratio(1.4, 0.9, 2.2),
			levels: 1, target: TargetLittleOrNoDifference, band: []float64{0.75, 1.25}, sizeChecked: true,
		},
		{
			name: "estimate below the band: the interval includes no effect, so no direction", in: ratio(0.6, 0.4, 1.1),
			levels: 1, target: TargetLittleOrNoDifference, band: []float64{0.75, 1.25}, sizeChecked: true,
		},
		{
			name:   "OR uses the lower cutoff for a wide interval",
			in:     Input{Measure: MeasureOddsRatio, Estimate: 0.5, CILow: 0.3, CIHigh: 0.8},
			levels: 1, target: TargetLower, sizeChecked: true,
			reason: "CI 0.3 to 0.8 excludes no effect but is wide: the upper limit is 2.67 times the lower (cutoff 2.5)",
		},
		{
			name:   "SMD includes no effect and crosses one threshold",
			in:     Input{Measure: MeasureSMD, Estimate: 0.1, CILow: -0.1, CIHigh: 0.3},
			levels: 1, target: TargetLittleOrNoDifference, band: []float64{-0.2, 0.2}, sizeChecked: true,
			reason: "CI -0.1 to 0.3 includes no effect and crosses the 0.2 threshold; " +
				"the estimate 0.1 leans toward an increase, but the interval includes no effect",
		},
		{
			name:   "SMD reaching past both thresholds: the evidence cannot tell",
			in:     Input{Measure: MeasureSMD, Estimate: -0.1, CILow: -0.6, CIHigh: 0.5},
			levels: 2, target: TargetUncertain, band: []float64{-0.2, 0.2}, sizeChecked: true,
		},
		{
			name:   "SMD reaching past both thresholds, limits in the wrong order",
			in:     Input{Measure: MeasureSMD, Estimate: -0.1, CILow: 0.5, CIHigh: -0.6},
			levels: 2, target: TargetUncertain, band: []float64{-0.2, 0.2}, sizeChecked: true,
		},
		{
			name:   "SMD within the thresholds is little or no difference with no lean",
			in:     Input{Measure: MeasureSMD, Estimate: 0.05, CILow: -0.1, CIHigh: 0.15},
			levels: 0, target: TargetLittleOrNoDifference, band: []float64{-0.2, 0.2}, sizeChecked: true,
			reason: "CI -0.1 to 0.15 includes no effect and lies within the -0.2 to 0.2 thresholds",
		},
		{
			name:   "SMD excludes no effect with few participants",
			in:     Input{Measure: MeasureSMD, Estimate: -0.5, CILow: -0.8, CIHigh: -0.2, Participants: 200},
			levels: 2, target: TargetLower, sizeChecked: true,
			reason: "CI -0.8 to -0.2 excludes no effect; 200 participants, far below the optimal information size of about 800",
		},
		{
			name:   "SMD excludes no effect with a middling number of participants",
			in:     Input{Measure: MeasureSMD, Estimate: -0.5, CILow: -0.8, CIHigh: -0.2, Participants: 500},
			levels: 1, target: TargetLower, sizeChecked: true,
		},
		{
			name:   "SMD excludes no effect and the size is just short",
			in:     Input{Measure: MeasureSMD, Estimate: -0.5, CILow: -0.8, CIHigh: -0.2, Participants: 799},
			levels: 1, target: TargetLower, sizeChecked: true,
		},
		{
			name:   "SMD excludes no effect and the size is just enough",
			in:     Input{Measure: MeasureSMD, Estimate: -0.5, CILow: -0.8, CIHigh: -0.2, Participants: 800},
			levels: 0, target: TargetLower, sizeChecked: true,
		},
		{
			name:   "SMD excludes no effect and the size is just under the very small cutoff",
			in:     Input{Measure: MeasureSMD, Estimate: -0.5, CILow: -0.8, CIHigh: -0.2, Participants: 239},
			levels: 2, target: TargetLower, sizeChecked: true,
		},
		{
			name:   "SMD excludes no effect with many participants",
			in:     Input{Measure: MeasureSMD, Estimate: -0.5, CILow: -0.8, CIHigh: -0.2, Participants: 1000},
			levels: 0, target: TargetLower, sizeChecked: true,
		},
		{
			name:   "SMD excludes no effect with unknown participants",
			in:     Input{Measure: MeasureSMD, Estimate: -0.5, CILow: -0.8, CIHigh: -0.2},
			levels: 0, target: TargetLower, sizeChecked: false,
			reason: "CI -0.8 to -0.2 excludes no effect; the number of participants is unknown, " +
				"so the information size was not checked",
		},
		{
			name:   "MD without an SD has no threshold: one level, no difference seen",
			in:     Input{Measure: MeasureMD, Estimate: 0.87, CILow: -1.58, CIHigh: 3.32},
			levels: 1, target: TargetLittleOrNoDifference, sizeChecked: true,
			reason: "CI -1.58 to 3.32 includes no effect and no threshold for a trivial effect is known " +
				"(the outcome's SD is missing); " +
				"the estimate 0.87 leans toward an increase, but the interval includes no effect",
		},
		{
			name:   "MD without an SD, CI -1 to 3",
			in:     Input{Measure: MeasureMD, Estimate: 1, CILow: -1, CIHigh: 3},
			levels: 1, target: TargetLittleOrNoDifference, sizeChecked: true,
			reason: "CI -1 to 3 includes no effect and no threshold for a trivial effect is known " +
				"(the outcome's SD is missing); " +
				"the estimate 1 leans toward an increase, but the interval includes no effect",
		},
		{
			name:   "MD without an SD, CI -1 to 3, limits in the wrong order and a negative lean",
			in:     Input{Measure: MeasureMD, Estimate: -0.5, CILow: 3, CIHigh: -1},
			levels: 1, target: TargetLittleOrNoDifference, sizeChecked: true,
			reason: "CI -1 to 3 includes no effect and no threshold for a trivial effect is known " +
				"(the outcome's SD is missing); " +
				"the estimate -0.5 leans toward a decrease, but the interval includes no effect",
		},
		{
			name:   "MD without an SD and exactly no effect names no lean",
			in:     Input{Measure: MeasureMD, Estimate: 0, CILow: -1, CIHigh: 1},
			levels: 1, target: TargetLittleOrNoDifference, sizeChecked: true,
			reason: "CI -1 to 1 includes no effect and no threshold for a trivial effect is known " +
				"(the outcome's SD is missing)",
		},
		{
			name:   "MD with an SD reaching past both thresholds: the evidence cannot tell",
			in:     Input{Measure: MeasureMD, Estimate: 0.5, CILow: -5, CIHigh: 6, SD: 10},
			levels: 2, target: TargetUncertain, band: []float64{-2, 2}, sizeChecked: true,
		},
		{
			name:   "MD with an SD has a band of 0.2 SD",
			in:     Input{Measure: MeasureMD, Estimate: 1, CILow: -1.5, CIHigh: 1.8, SD: 10},
			levels: 0, target: TargetLittleOrNoDifference, band: []float64{-2, 2}, sizeChecked: true,
		},
		{
			name:   "MD with an SD and an interval reaching past the band",
			in:     Input{Measure: MeasureMD, Estimate: 1, CILow: -1.5, CIHigh: 2.5, SD: 10},
			levels: 1, target: TargetLittleOrNoDifference, band: []float64{-2, 2}, sizeChecked: true,
		},
		{
			name:   "MD excluding no effect uses the information size",
			in:     Input{Measure: MeasureMD, Estimate: 5, CILow: 2, CIHigh: 8, Participants: 100, SD: 10},
			levels: 2, target: TargetHigher, sizeChecked: true,
		},
		{
			name: "an interval limit exactly on a band edge is not crossed", in: ratio(1.0, 0.75, 1.25),
			levels: 0, target: TargetLittleOrNoDifference, band: []float64{0.75, 1.25}, sizeChecked: true,
		},
		{
			name: "an interval limit exactly on the null counts as including it", in: ratio(1.2, 1, 1.4),
			levels: 1, target: TargetLittleOrNoDifference, band: []float64{0.75, 1.25}, sizeChecked: true,
		},
		{
			name: "the limits in the wrong order are put right", in: ratio(0.77, 1.23, 0.48),
			levels: 1, target: TargetLittleOrNoDifference, band: []float64{0.75, 1.25}, sizeChecked: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AssessImprecision(tc.in)
			require.NoError(t, err)

			assert.Equal(t, tc.levels, got.Levels, "levels: %s", got.Reason)
			assert.Equal(t, tc.target, got.Target)
			assert.Equal(t, tc.sizeChecked, got.SizeChecked)
			assert.NotEmpty(t, got.Reason)

			if tc.reason != "" {
				assert.Equal(t, tc.reason, got.Reason)
			}

			if tc.band == nil {
				// no band is used: unknown, or the null is the threshold
				assert.Nil(t, got.BandLow)
				assert.Nil(t, got.BandHigh)

				return
			}

			require.NotNil(t, got.BandLow)
			require.NotNil(t, got.BandHigh)
			assert.InDelta(t, tc.band[0], *got.BandLow, 1e-9)
			assert.InDelta(t, tc.band[1], *got.BandHigh, 1e-9)
		})
	}
}

func TestAssessImprecisionRejectsInvalidInput(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)

	cases := map[string]Input{
		"unknown measure":           {Estimate: 1, CILow: 0.5, CIHigh: 2},
		"estimate below the CI":     ratio(0.4, 0.5, 2),
		"estimate above the CI":     ratio(2.5, 0.5, 2),
		"a zero ratio limit":        ratio(0.5, 0, 2),
		"a negative ratio":          ratio(-1, -2, -0.5),
		"NaN estimate":              ratio(nan, 0.5, 2),
		"infinite limit":            ratio(1, 0.5, inf),
		"NaN limit":                 ratio(1, nan, 2),
		"negative SD":               {Measure: MeasureMD, Estimate: 1, CILow: 0, CIHigh: 2, SD: -1},
		"infinite SD":               {Measure: MeasureMD, Estimate: 1, CILow: 0, CIHigh: 2, SD: inf},
		"negative participants":     {Measure: MeasureSMD, Estimate: 1, CILow: 0, CIHigh: 2, Participants: -5},
		"difference estimate range": {Measure: MeasureSMD, Estimate: 3, CILow: 0, CIHigh: 2},
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := AssessImprecision(in)
			require.ErrorIs(t, err, errInvalid)
		})
	}
}

func TestNum(t *testing.T) {
	for in, want := range map[float64]string{
		0:                  "0",
		0.996:              "0.996",
		1.2000000000000002: "1.2",
		12.26:              "12.3",
		-0.8:               "-0.8",
		1234:               "1230",
		0.00123456:         "0.00123",
	} {
		assert.Equal(t, want, num(in))
	}
}

func FuzzAssessImprecision(f *testing.F) {
	f.Add(uint8(0), 0.77, 0.48, 1.23, 0, 0.0)
	f.Add(uint8(1), 0.5, 0.3, 0.8, 0, 0.0)
	f.Add(uint8(2), -0.5, -0.8, -0.2, 200, 0.0)
	f.Add(uint8(3), 1.0, -1.5, 1.8, 0, 10.0)
	f.Add(uint8(0), math.NaN(), 0.0, math.Inf(1), -1, -1.0)

	measures := []Measure{MeasureRatio, MeasureOddsRatio, MeasureSMD, MeasureMD, ""}

	f.Fuzz(func(t *testing.T, m uint8, est, low, high float64, n int, sd float64) {
		got, err := AssessImprecision(Input{
			Measure: measures[int(m)%len(measures)], Estimate: est, CILow: low, CIHigh: high, Participants: n, SD: sd,
		})
		if err != nil {
			return
		}

		assert.GreaterOrEqual(t, got.Levels, 0)
		assert.LessOrEqual(t, got.Levels, maxLevels)
		assert.Contains(t, []Target{TargetLower, TargetHigher, TargetLittleOrNoDifference, TargetUncertain}, got.Target)

		assert.NotEmpty(t, got.Reason)
		assert.Equal(t, got.BandLow == nil, got.BandHigh == nil)

		// an interval that includes no effect never gives a direction: it is
		// little or no difference, or uncertain when two thresholds are crossed
		if strings.Contains(got.Reason, "includes no effect") {
			assert.Contains(t, []Target{TargetLittleOrNoDifference, TargetUncertain}, got.Target)
			assert.Equal(t, got.Levels == maxLevels, got.Target == TargetUncertain)
		}
	})
}
