// Package meta is the arithmetic of a meta-analysis: effects of single
// studies on an analysis scale (log ratios, differences) with their standard
// errors, and the DerSimonian-Laird random-effects pool. No Go library covers
// it (gonum has none; a search of pkg.go.dev and GitHub finds no maintained
// one), and the method is a page of formulas, so it is implemented here and
// checked against a published worked example.
package meta

import (
	"errors"
	"fmt"
	"math"
)

// Bounds of a reported confidence level, in percent: a level of 50% or less
// is not a confidence interval anyone reports, and above 99.9% the normal
// quantile is dominated by tail error.
const (
	minLevel = 50.0
	maxLevel = 99.9
)

// DefaultLevel is the confidence level, in percent, of an interval whose level
// the study does not state.
const DefaultLevel = 95.0

// zInterval is the exact normal quantile (97.5%) of the pooled interval, as
// R's metafor uses it.
const zInterval = 1.959963984540054

// continuityCorrection is added to every cell of a 2x2 table that has a zero.
const continuityCorrection = 0.5

var (
	// ErrNoEvents marks a study without a measurable effect: no events in
	// either arm, or every participant had the event. Such a study carries no
	// information about the ratio and is left out.
	ErrNoEvents = errors.New("no events or all events in both arms")
	// ErrInvalid marks numbers that cannot be an effect (negative counts, a
	// non-positive SD, an interval that does not contain the estimate).
	ErrInvalid = errors.New("invalid numbers")
	// ErrNoStudies is returned when there is nothing to pool.
	ErrNoStudies = errors.New("no studies to pool")
)

// Study is one study's effect on the analysis scale (a log ratio, a mean
// difference, a standardized mean difference) with its standard error.
type Study struct {
	Y, SE float64
}

// Result is a pooled effect on the analysis scale.
type Result struct {
	Estimate, SE, CILow, CIHigh float64
	K                           int
	// Tau2, Q and I2 describe the heterogeneity (I2 in percent); they are zero
	// for a single study.
	Tau2, Q, I2 float64
	// Weights are the studies' shares of the pooled estimate (random-effects
	// weights, summing to 1), in the order the studies were given.
	Weights []float64
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// LogRR is the log risk ratio of two arms (the first is the intervention)
// with the standard error. A 2x2 table with a zero cell gets the 0.5
// continuity correction in every cell; a table without events in either arm
// (or with events in everyone) is ErrNoEvents.
func LogRR(events1, total1, events2, total2 float64) (Study, error) {
	if events1 < 0 || events2 < 0 || total1 <= 0 || total2 <= 0 || events1 > total1 || events2 > total2 {
		return Study{}, invalid("counts %v/%v vs %v/%v", events1, total1, events2, total2)
	}

	if (events1 == 0 && events2 == 0) || (events1 == total1 && events2 == total2) {
		return Study{}, ErrNoEvents
	}

	if events1 == 0 || events2 == 0 || events1 == total1 || events2 == total2 {
		events1 += continuityCorrection
		events2 += continuityCorrection
		total1 += 2 * continuityCorrection
		total2 += 2 * continuityCorrection
	}

	return Study{
		Y:  math.Log(events1/total1) - math.Log(events2/total2),
		SE: math.Sqrt(1/events1 - 1/total1 + 1/events2 - 1/total2),
	}, nil
}

// LogOR is the log odds ratio of two arms (the first is the intervention) with
// the standard error of the Woolf method, sqrt(1/a + 1/b + 1/c + 1/d) over the
// cells of the 2x2 table. The zero-cell convention is LogRR's: a table with a
// zero cell (no events, or events in everyone, in either arm) gets 0.5 added to
// every cell, and a table without events in either arm (or with events in
// everyone) is ErrNoEvents. The same counts therefore stay usable as an RR or
// as an OR under one rule.
func LogOR(events1, total1, events2, total2 float64) (Study, error) {
	if events1 < 0 || events2 < 0 || total1 <= 0 || total2 <= 0 || events1 > total1 || events2 > total2 {
		return Study{}, invalid("counts %v/%v vs %v/%v", events1, total1, events2, total2)
	}

	if (events1 == 0 && events2 == 0) || (events1 == total1 && events2 == total2) {
		return Study{}, ErrNoEvents
	}

	// the cells: events and non-events of the intervention, then of the control
	a, b, c, d := events1, total1-events1, events2, total2-events2

	if a == 0 || b == 0 || c == 0 || d == 0 {
		a, b, c, d = a+continuityCorrection, b+continuityCorrection, c+continuityCorrection, d+continuityCorrection
	}

	return Study{
		Y:  math.Log(a*d) - math.Log(b*c),
		SE: math.Sqrt(1/a + 1/b + 1/c + 1/d),
	}, nil
}

// Arm is the mean, SD and size of one arm of a continuous outcome.
type Arm struct {
	Mean, SD, N float64
}

func (a Arm) valid() bool {
	return a.SD > 0 && a.N >= 2 && !math.IsNaN(a.Mean) && !math.IsInf(a.Mean, 0)
}

// MD is the mean difference (first arm minus second) with its standard error.
func MD(a, b Arm) (Study, error) {
	if !a.valid() || !b.valid() {
		return Study{}, invalid("arms %+v and %+v", a, b)
	}

	return Study{Y: a.Mean - b.Mean, SE: math.Sqrt(a.SD*a.SD/a.N + b.SD*b.SD/b.N)}, nil
}

// HedgesG is the standardized mean difference with Hedges' small-sample
// correction (first arm minus second), standard error from the usual
// large-sample variance.
func HedgesG(a, b Arm) (Study, error) {
	if !a.valid() || !b.valid() {
		return Study{}, invalid("arms %+v and %+v", a, b)
	}

	df := a.N + b.N - 2
	g := (1 - 3/(4*df-1)) * (a.Mean - b.Mean) / pooledSD(a, b)
	variance := 1/a.N + 1/b.N + g*g/(2*(a.N+b.N))

	return Study{Y: g, SE: math.Sqrt(variance)}, nil
}

// PooledSD is the standard deviation of the outcome across both arms of a
// study, weighted by the degrees of freedom of each arm.
func PooledSD(a, b Arm) (float64, error) {
	if !a.valid() || !b.valid() {
		return 0, invalid("arms %+v and %+v", a, b)
	}

	return pooledSD(a, b), nil
}

// pooledSD expects valid arms.
func pooledSD(a, b Arm) float64 {
	return math.Sqrt(((a.N-1)*a.SD*a.SD + (b.N-1)*b.SD*b.SD) / (a.N + b.N - 2))
}

// ZForLevel is the two-sided normal quantile of a confidence level given in
// percent: z = Phi^-1(1 - (1 - level/100) / 2), which equals
// sqrt(2) * erfinv(level/100). 95 gives 1.959964. A level outside (50, 99.9]
// is ErrInvalid.
func ZForLevel(level float64) (float64, error) {
	if math.IsNaN(level) || level <= minLevel || level > maxLevel {
		return 0, invalid("confidence level %v%% (want above %v and up to %v)", level, minLevel, maxLevel)
	}

	return math.Sqrt2 * math.Erfinv(level/100), nil
}

// RatioCI turns a reported ratio (RR, OR, HR) with its confidence interval
// of the given level (percent) into a log-scale effect:
// SE = (ln high - ln low) / (2 * z), z the quantile of the level.
func RatioCI(value, low, high, level float64) (Study, error) {
	z, err := ZForLevel(level)
	if err != nil {
		return Study{}, err
	}

	if value <= 0 || low <= 0 || high <= low || value < low || value > high {
		return Study{}, invalid("ratio %v (%v to %v)", value, low, high)
	}

	return Study{Y: math.Log(value), SE: (math.Log(high) - math.Log(low)) / (2 * z)}, nil
}

// DifferenceCI turns a reported difference (MD, SMD) with its confidence
// interval of the given level (percent) into an effect:
// SE = (high - low) / (2 * z).
func DifferenceCI(value, low, high, level float64) (Study, error) {
	z, err := ZForLevel(level)
	if err != nil {
		return Study{}, err
	}

	if high <= low || value < low || value > high || math.IsNaN(value) || math.IsInf(value, 0) {
		return Study{}, invalid("difference %v (%v to %v)", value, low, high)
	}

	return Study{Y: value, SE: (high - low) / (2 * z)}, nil
}

func checkStudies(studies []Study) error {
	if len(studies) == 0 {
		return ErrNoStudies
	}

	for _, s := range studies {
		if math.IsNaN(s.SE) || s.SE <= 0 || math.IsInf(s.SE, 0) || math.IsNaN(s.Y) || math.IsInf(s.Y, 0) {
			return invalid("study effect %v with standard error %v", s.Y, s.SE)
		}
	}

	return nil
}

// Single is the effect of one study with its 95% interval: nothing is pooled.
func Single(s Study) (Result, error) {
	if err := checkStudies([]Study{s}); err != nil {
		return Result{}, err
	}

	return Result{
		Estimate: s.Y, SE: s.SE, CILow: s.Y - zInterval*s.SE, CIHigh: s.Y + zInterval*s.SE, K: 1,
		Weights: []float64{1},
	}, nil
}

// Pool combines two or more studies with the DerSimonian-Laird random-effects
// model: the between-study variance comes from the method of moments,
// negative values are set to zero, and the interval is the normal one.
func Pool(studies []Study) (Result, error) {
	if err := checkStudies(studies); err != nil {
		return Result{}, err
	}

	k := float64(len(studies))
	if k < 2 {
		return Result{}, invalid("pooling needs at least two studies, got %d", len(studies))
	}

	var sumW, sumW2, sumWY float64

	for _, s := range studies {
		w := 1 / (s.SE * s.SE)
		sumW += w
		sumW2 += w * w
		sumWY += w * s.Y
	}

	fixed := sumWY / sumW

	var q float64

	for _, s := range studies {
		w := 1 / (s.SE * s.SE)
		q += w * (s.Y - fixed) * (s.Y - fixed)
	}

	tau2 := math.Max(0, (q-(k-1))/(sumW-sumW2/sumW))

	var sumWr, sumWrY float64

	weights := make([]float64, len(studies))

	for i, s := range studies {
		w := 1 / (s.SE*s.SE + tau2)
		weights[i] = w
		sumWr += w
		sumWrY += w * s.Y
	}

	for i := range weights {
		weights[i] /= sumWr
	}

	est, se := sumWrY/sumWr, math.Sqrt(1/sumWr)

	i2 := 0.0
	if q > 0 {
		i2 = math.Max(0, (q-(k-1))/q) * 100
	}

	return Result{
		Estimate: est, SE: se, CILow: est - zInterval*se, CIHigh: est + zInterval*se,
		K: len(studies), Tau2: tau2, Q: q, I2: i2, Weights: weights,
	}, nil
}
