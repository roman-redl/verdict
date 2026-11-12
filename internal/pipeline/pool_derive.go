package pipeline

// Putting one study's extracted numbers (model.Effect) on an analysis scale
// for pooling: counts, means or a reported estimate with its interval.

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/roman-redl/verdict/internal/meta"
	"github.com/roman-redl/verdict/internal/model"
)

// maxOutcomeRunes caps the joined outcome texts of a pool.
const maxOutcomeRunes = 200

// outcomeLabel is "outcome (timepoint)" with the spaces collapsed; empty when
// the critic named no outcome.
func outcomeLabel(ef *model.Effect) string {
	o := tidyUnit(ef.Outcome)
	if o == "" {
		return ""
	}

	if tp := tidyUnit(ef.Timepoint); tp != "" {
		return o + " (" + tp + ")"
	}

	return o
}

// joinOutcomes lists the distinct outcome texts of a pool in study order,
// separated by " / " and cut to a length that stays readable.
func joinOutcomes(items []pooledStudy) string {
	var seen []string

	for i := range items {
		o := items[i].outcome
		if o != "" && !slices.ContainsFunc(seen, func(s string) bool { return strings.EqualFold(s, o) }) {
			seen = append(seen, o)
		}
	}

	return truncate(strings.Join(seen, " / "), maxOutcomeRunes)
}

// deriveEffect puts one study's extracted numbers on an analysis scale:
// counts first, then means, then a reported estimate with its interval. The
// critic's labels of the outcome's direction ride along: the unified SMD pool
// reads them.
func deriveEffect(ef *model.Effect) (pooledStudy, error) {
	ps, err := deriveNumbers(ef)
	if err != nil {
		return pooledStudy{}, err
	}

	ps.higherIsBetter, ps.eventIsHarm, ps.metric = ef.HigherIsBetter, ef.EventIsHarm, ef.Metric

	return ps, nil
}

// deriveNumbers is the arithmetic of deriveEffect.
func deriveNumbers(ef *model.Effect) (pooledStudy, error) {
	if ef.Intervention != nil && ef.Control != nil {
		a, b, ok, err := countArms(ef.Intervention, ef.Control)
		if err != nil {
			return pooledStudy{}, fmt.Errorf("counts: %w", err)
		}

		if ok {
			return deriveCounts(a, b)
		}

		if a, b, ok := meanArms(ef.Intervention, ef.Control); ok {
			return deriveContinuous(a, b, ef.Unit)
		}
	}

	if ef.Measure != "" {
		return deriveReported(ef)
	}

	return pooledStudy{}, errNoNumbers
}

// deriveCounts turns the counts of both arms into the study's log RR (its
// default measure) and the log OR it can be pooled as instead.
func deriveCounts(a, b countedArm) (pooledStudy, error) {
	rr, err := meta.LogRR(a.events, a.total, b.events, b.total)
	if err != nil {
		return pooledStudy{}, fmt.Errorf("counts: %w", err)
	}

	// LogOR rejects exactly the counts LogRR rejects, so this error is not
	// expected; it is returned all the same instead of dropping the OR silently
	or, err := meta.LogOR(a.events, a.total, b.events, b.total)
	if err != nil {
		return pooledStudy{}, fmt.Errorf("counts: %w", err)
	}

	return pooledStudy{
		// rounded: the totals of a cluster trial are divided by its design effect
		measure: model.MeasureRR, study: rr, fromCounts: true, or: or, n: int(math.Round(a.total + b.total)),
		converted: strings.Join(slices.DeleteFunc([]string{a.derived, b.derived},
			func(s string) bool { return s == "" }), "; "),
	}, nil
}

// countedArm is the events and total of one arm; derived describes the count
// when it was computed from a percentage.
type countedArm struct {
	events, total float64
	derived       string
}

// countArms returns the events and totals of both arms when both can be
// counted. An error means a percentage that cannot be one.
func countArms(i, c *model.Arm) (a, b countedArm, ok bool, err error) {
	a, okA, err := countArm(i, "intervention")
	if err != nil {
		return a, b, false, err
	}

	b, okB, err := countArm(c, "control")
	if err != nil {
		return a, b, false, err
	}

	return a, b, okA && okB, nil
}

// countArm reads one arm: events and total as given, or, when the study
// reports only a percentage of the arm, events = round(percent / 100 * total).
// A percentage outside 0-100 is an error; given events win over a percentage.
func countArm(a *model.Arm, label string) (countedArm, bool, error) {
	if a.Total == nil {
		return countedArm{}, false, nil
	}

	if a.Events != nil {
		return countedArm{events: *a.Events, total: *a.Total}, true, nil
	}

	if a.Percent == nil {
		return countedArm{}, false, nil
	}

	pct := *a.Percent
	if math.IsNaN(pct) || pct < 0 || pct > 100 {
		return countedArm{}, false, fmt.Errorf("%s arm: percent %v is outside 0-100", label, pct)
	}

	events := math.Round(pct / 100 * *a.Total)

	return countedArm{
		events: events, total: *a.Total,
		derived: fmt.Sprintf("%s %v%% of %v = %v events", label, pct, *a.Total, events),
	}, true, nil
}

// meanArms returns the means, SDs and sizes of both arms when all are given.
func meanArms(i, c *model.Arm) (a, b meta.Arm, ok bool) {
	if i.Mean == nil || i.SD == nil || i.N == nil || c.Mean == nil || c.SD == nil || c.N == nil {
		return a, b, false
	}

	return meta.Arm{Mean: *i.Mean, SD: *i.SD, N: *i.N}, meta.Arm{Mean: *c.Mean, SD: *c.SD, N: *c.N}, true
}

func deriveContinuous(a, b meta.Arm, unit string) (pooledStudy, error) {
	md, err := meta.MD(a, b)
	if err != nil {
		return pooledStudy{}, fmt.Errorf("means: %w", err)
	}

	smd, err := meta.HedgesG(a, b)
	if err != nil {
		return pooledStudy{}, fmt.Errorf("means: %w", err)
	}

	sd, err := meta.PooledSD(a, b)
	if err != nil {
		return pooledStudy{}, fmt.Errorf("means: %w", err)
	}

	return pooledStudy{
		continuous: true, unit: tidyUnit(unit), md: md, smd: smd, sd: sd, n: int(math.Round(a.N + b.N)),
	}, nil
}

// deriveReported handles a study that reports an estimate and its confidence
// interval, at the stated level (95% when none is stated).
func deriveReported(ef *model.Effect) (pooledStudy, error) {
	m, err := model.ParseMeasure(ef.Measure)
	if err != nil {
		return pooledStudy{}, fmt.Errorf("estimate: %w", err)
	}

	if ef.Value == nil || ef.CILow == nil || ef.CIHigh == nil {
		return pooledStudy{}, fmt.Errorf("estimate %s: %w: the value or its interval is missing", m, errNoNumbers)
	}

	ps := pooledStudy{measure: m}

	level := meta.DefaultLevel
	if ef.CILevel != nil {
		level = *ef.CILevel
	}

	value, low, high := *ef.Value, *ef.CILow, *ef.CIHigh

	if ef.Reversed {
		value, low, high, err = reverseEstimate(m, value, low, high)
		if err != nil {
			return pooledStudy{}, fmt.Errorf("estimate %s: %w", m, err)
		}

		ps.flipped = fmt.Sprintf("%s %v (%v to %v) -> %v (%v to %v)",
			m, *ef.Value, *ef.CILow, *ef.CIHigh, value, low, high)
	}

	if m.IsRatio() {
		ps.study, err = meta.RatioCI(value, low, high, level)
	} else {
		ps.study, err = meta.DifferenceCI(value, low, high, level)
	}

	if err != nil {
		return pooledStudy{}, fmt.Errorf("estimate %s: %w", m, err)
	}

	if m == model.MeasureMD {
		// a mean difference is poolable only with studies in the same unit
		if ps.unit = tidyUnit(ef.Unit); ps.unit == "" {
			return pooledStudy{}, errors.New("estimate MD: the unit is not stated")
		}
	}

	return ps, nil
}

// reverseEstimate turns an estimate of the comparator versus the intervention
// into the intervention versus the comparator: a ratio becomes 1/value with the
// interval 1/high .. 1/low, a difference becomes -value with -high .. -low.
func reverseEstimate(m model.Measure, value, low, high float64) (v, lo, hi float64, err error) {
	if !m.IsRatio() {
		return -value, -high, -low, nil
	}

	if value <= 0 || low <= 0 || high <= 0 {
		return 0, 0, 0, fmt.Errorf("ratio %v (%v to %v) cannot be reversed: it must be positive", value, low, high)
	}

	return 1 / value, 1 / high, 1 / low, nil
}
