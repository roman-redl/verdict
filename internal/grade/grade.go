// Package grade is the GRADE imprecision rule applied to a pooled estimate:
// how many levels of certainty the width of the confidence interval (and, for
// continuous outcomes, the amount of data) costs, and which conclusion that
// applies to. Pure arithmetic with its own small types, like internal/meta:
// the pipeline maps its domain types onto them. The rules follow Core GRADE
// (BMJ 2025) and the Cochrane Handbook, chapter 14; the thresholds are the
// defaults for the case when no minimal important difference is known.
package grade

import (
	"errors"
	"fmt"
	"math"
	"strconv"
)

// Measure is the kind of the effect measure; it decides the null value, the
// band of a trivial effect and which rule applies once the interval excludes
// no effect.
type Measure string

const (
	// MeasureRatio is a ratio other than the odds ratio (RR, HR).
	MeasureRatio Measure = "ratio"
	// MeasureOddsRatio is the odds ratio: it runs further from 1 than a risk
	// ratio for the same effect, so its "wide interval" cutoff is lower.
	MeasureOddsRatio Measure = "odds_ratio"
	// MeasureSMD is the standardized mean difference.
	MeasureSMD Measure = "smd"
	// MeasureMD is the mean difference in the outcome's own unit.
	MeasureMD Measure = "md"
)

func (m Measure) isRatio() bool { return m == MeasureRatio || m == MeasureOddsRatio }

// Target is the conclusion the imprecision levels apply to.
type Target string

const (
	// TargetLower is a lower value of the outcome under the intervention.
	TargetLower Target = "lower"
	// TargetHigher is a higher value of the outcome under the intervention.
	TargetHigher Target = "higher"
	// TargetLittleOrNoDifference is the conclusion that there is little or
	// no difference.
	TargetLittleOrNoDifference Target = "little_or_no_difference"
	// TargetUncertain means the evidence cannot tell: the interval includes no
	// effect and reaches past two thresholds of a trivial effect, so the
	// sign of the point estimate must not pass for a direction.
	TargetUncertain Target = "uncertain"
)

// Default thresholds.
const (
	// A ratio below bandRatioLow or above bandRatioHigh is an important effect.
	bandRatioLow  = 0.75
	bandRatioHigh = 1.25
	// bandSMD is the trivial-effect limit of an SMD; the limit of an MD is
	// the same share of the outcome's SD.
	bandSMD = 0.2

	// A confidence interval that excludes no effect is called wide when its
	// upper limit is this many times its lower limit.
	wideRatio     = 3.0
	wideOddsRatio = 2.5

	// optimalSize is the rule of thumb for the optimal information size of a
	// continuous outcome; under verySmallSize the data fall far short of it.
	optimalSize   = 800
	verySmallSize = 240

	maxLevels = 2
)

// Input is a pooled estimate on the natural scale (ratios are not logged).
type Input struct {
	Measure       Measure
	Estimate      float64
	CILow, CIHigh float64
	Participants  int     // 0 = unknown
	SD            float64 // of an MD's outcome; 0 = unknown
}

// Assessment is the imprecision rating of one pooled estimate.
type Assessment struct {
	// Levels is how many levels to rate down for imprecision: 0, 1 or 2.
	Levels int
	// Target is the conclusion the levels apply to.
	Target Target
	// BandLow and BandHigh bound the range of trivial effects that was used;
	// nil for a mean difference without an SD, and when the interval excludes
	// no effect (the null itself is the threshold then).
	BandLow, BandHigh *float64
	// Reason is one short sentence with the numbers behind the levels; for an
	// interval that includes no effect and is not inside the band it also says
	// where the point estimate leans.
	Reason string
	// SizeChecked is false only when the information-size rule was needed
	// but could not be applied because the number of participants is unknown.
	SizeChecked bool
}

var errInvalid = errors.New("invalid input")

// band is the range of trivial effects around the null.
type band struct {
	low, high float64
	known     bool
}

// AssessImprecision rates the imprecision of an estimate. The interval's
// limits may come in either order.
func AssessImprecision(in Input) (Assessment, error) {
	in, err := normalize(in)
	if err != nil {
		return Assessment{}, err
	}

	null := nullOf(in.Measure)
	if in.CILow <= null && null <= in.CIHigh {
		return includesNull(in, bandOf(in)), nil
	}

	return excludesNull(in, null), nil
}

// normalize orders the interval and rejects numbers that cannot be a result.
func normalize(in Input) (Input, error) {
	switch in.Measure {
	case MeasureRatio, MeasureOddsRatio, MeasureSMD, MeasureMD:
	default:
		return in, fmt.Errorf("%w: unknown measure %q", errInvalid, in.Measure)
	}

	for _, v := range []float64{in.Estimate, in.CILow, in.CIHigh, in.SD} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return in, fmt.Errorf("%w: a number is not finite", errInvalid)
		}
	}

	if in.CILow > in.CIHigh {
		in.CILow, in.CIHigh = in.CIHigh, in.CILow
	}

	if in.Estimate < in.CILow || in.Estimate > in.CIHigh {
		return in, fmt.Errorf("%w: the estimate %s is outside its interval %s to %s",
			errInvalid, num(in.Estimate), num(in.CILow), num(in.CIHigh))
	}

	if in.Measure.isRatio() && in.CILow <= 0 {
		return in, fmt.Errorf("%w: a ratio must be positive, the interval starts at %s", errInvalid, num(in.CILow))
	}

	if in.Participants < 0 || in.SD < 0 {
		return in, fmt.Errorf("%w: the number of participants and the SD cannot be negative", errInvalid)
	}

	return in, nil
}

func nullOf(m Measure) float64 {
	if m.isRatio() {
		return 1
	}

	return 0
}

// bandOf is the band of trivial effects; an MD has one only with an SD.
func bandOf(in Input) band {
	switch in.Measure {
	case MeasureRatio, MeasureOddsRatio:
		return band{low: bandRatioLow, high: bandRatioHigh, known: true}
	case MeasureSMD:
		return band{low: -bandSMD, high: bandSMD, known: true}
	case MeasureMD:
		if in.SD > 0 {
			return band{low: -bandSMD * in.SD, high: bandSMD * in.SD, known: true}
		}
	}

	return band{}
}

// includesNull rates an interval that includes no effect. The point estimate
// never gives the direction then (strict GRADE would say "may increase"; the
// Cochrane practice is "no difference seen"). With a band, the question is
// whether the interval stays within the trivial effects (little or no
// difference, precisely) or reaches past band edges, each edge crossed being
// one level. Without a band (a mean difference with no SD) nothing tells
// little or no difference from a small effect: it counts as one level, as if
// one edge were crossed. Levels 0 and 1 conclude little or no difference and
// name where the estimate leans; levels 2 conclude that the evidence cannot
// tell.
func includesNull(in Input, b band) Assessment {
	a := Assessment{SizeChecked: true, Target: TargetLittleOrNoDifference}
	ci := fmt.Sprintf("CI %s to %s includes no effect", num(in.CILow), num(in.CIHigh))

	if !b.known {
		a.Levels = 1
		a.Reason = ci + " and no threshold for a trivial effect is known (the outcome's SD is missing)" + leanOf(in)

		return a
	}

	lo, hi := b.low, b.high
	a.BandLow, a.BandHigh = &lo, &hi

	var crossed []string

	for _, edge := range []float64{b.low, b.high} {
		if in.CILow < edge && edge < in.CIHigh {
			crossed = append(crossed, num(edge))
		}
	}

	a.Levels = min(len(crossed), maxLevels)

	switch len(crossed) {
	case 0:
		a.Reason = fmt.Sprintf("%s and lies within the %s to %s thresholds", ci, num(b.low), num(b.high))
	case 1:
		a.Reason = fmt.Sprintf("%s and crosses the %s threshold", ci, crossed[0]) + leanOf(in)
	default:
		a.Reason = fmt.Sprintf("%s and crosses the %s and %s thresholds", ci, crossed[0], crossed[1]) + leanOf(in)
		a.Target = TargetUncertain
	}

	return a
}

// leanOf is the clause that says where the point estimate leans when the
// interval does not exclude no effect, with a leading separator; empty when
// the estimate sits exactly at no effect. It names the sign only: the
// interval's levels say how much that lean is worth.
func leanOf(in Input) string {
	null := nullOf(in.Measure)

	var way string

	switch {
	case in.Estimate > null:
		way = "an increase"
	case in.Estimate < null:
		way = "a decrease"
	default:
		return ""
	}

	return fmt.Sprintf("; the estimate %s leans toward %s, but the interval includes no effect", num(in.Estimate), way)
}

// excludesNull rates an interval that excludes no effect: the null is the
// threshold, so only the width matters. For a ratio an interval spanning a
// large factor means few data behind a large effect; for a difference the
// amount of data is judged against the optimal information size.
func excludesNull(in Input, null float64) Assessment {
	a := Assessment{Target: TargetHigher, SizeChecked: true}
	if in.CIHigh < null {
		a.Target = TargetLower
	}

	ci := fmt.Sprintf("CI %s to %s excludes no effect", num(in.CILow), num(in.CIHigh))

	if in.Measure.isRatio() {
		a.Levels, a.Reason = ratioWidth(in, ci)
	} else {
		a.Levels, a.Reason, a.SizeChecked = informationSize(in.Participants, ci)
	}

	a.Levels = min(a.Levels, maxLevels)

	return a
}

func ratioWidth(in Input, ci string) (levels int, reason string) {
	limit := wideRatio
	if in.Measure == MeasureOddsRatio {
		limit = wideOddsRatio
	}

	width := in.CIHigh / in.CILow
	if width >= limit {
		return 1, fmt.Sprintf("%s but is wide: the upper limit is %s times the lower (cutoff %s)",
			ci, num(width), num(limit))
	}

	return 0, fmt.Sprintf("%s and is not wide: the upper limit is %s times the lower (cutoff %s)",
		ci, num(width), num(limit))
}

func informationSize(participants int, ci string) (levels int, reason string, checked bool) {
	switch {
	case participants == 0:
		return 0, ci + "; the number of participants is unknown, so the information size was not checked", false
	case participants < verySmallSize:
		return 2, fmt.Sprintf("%s; %d participants, far below the optimal information size of about %d",
			ci, participants, optimalSize), true
	case participants < optimalSize:
		return 1, fmt.Sprintf("%s; %d participants, below the optimal information size of about %d",
			ci, participants, optimalSize), true
	default:
		return 0, fmt.Sprintf("%s; %d participants, reaching the optimal information size of about %d",
			ci, participants, optimalSize), true
	}
}

// num formats a number for a reason sentence: three significant digits, no
// exponent, so that a computed 1.2000000000000002 reads 1.2.
func num(v float64) string {
	r, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'g', 3, 64), 64) // 'g' output always parses

	return strconv.FormatFloat(r, 'f', -1, 64)
}
