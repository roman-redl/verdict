package pipeline

// One scale for one construct. Studies of the same outcome (same construct, same
// time point, same design class — the eligibility rules of pool_select.go have
// already run) can still report it on different measures: one trial counts
// events, another gives the means of a rating scale. Cochrane puts them on one
// scale: a continuous outcome as Hedges' g, a binary one as ln(OR) x sqrt(3)/pi
// (Handbook 10.6), after every scale is pointed the same way (6.5.1.2) and
// without mixing final values with change scores (10.5.2). The critic labels the
// direction (model.Effect), Go does the arithmetic.

import (
	"github.com/roman-redl/verdict/internal/meta"
	"github.com/roman-redl/verdict/internal/model"
)

// outcomeStandardized is what the outcome label of a unified pool states: the
// effect is standardized and oriented, so that the claim is read against it.
const outcomeStandardized = "standardized, positive favours the intervention"

// standardizedOutcome is the outcome label of a unified pool: the critics' names
// of the outcome, then what the scale is.
func standardizedOutcome(outcomes string) string {
	if outcomes == "" {
		return "standardized effect, positive favours the intervention"
	}

	return outcomes + " (" + outcomeStandardized + ")"
}

// scaleSign is +1 when a higher value of the study's standardized effect (the
// intervention arm minus the control arm) is the better state, -1 when it is
// the worse one, and 0 when the critic could not tell: a continuous outcome by
// higher_is_better, a binary one by event_is_harm (more events of a harm is the
// worse state). A study that is neither (a reported estimate) has no sign.
func scaleSign(it *pooledStudy) float64 {
	switch {
	case it.continuous && it.higherIsBetter != nil:
		return boolSign(*it.higherIsBetter)
	case it.fromCounts && it.eventIsHarm != nil:
		return boolSign(!*it.eventIsHarm)
	}

	return 0
}

func boolSign(positive bool) float64 {
	if positive {
		return 1
	}

	return -1
}

// standardized is the study's effect on the unified scale, positive when the
// intervention is better; the sign is that of scaleSign. A study from counts is
// the log OR scaled to an SMD, a study from means is Hedges' g.
func (ps *pooledStudy) standardized(sign float64) meta.Study {
	s := ps.smd
	if ps.fromCounts {
		s = meta.SMDFromLogOR(ps.or)
	}

	if sign < 0 {
		return s.Reversed()
	}

	return s
}

// unifiedGroup is the candidate pool of the studies that can be put on one
// standardized scale: those with means, SDs and sizes, and those with counts,
// whose direction is known. Excluded studies are named in the second result
// (keyed by position in items) with the reason: the direction of the scale is
// unknown, or the means are of the minority metric. Studies that cannot be
// converted at all (a reported HR, RR or OR, an MD without SDs) are in neither:
// they keep their own measure as the reason when the pool is chosen.
func unifiedGroup(items []pooledStudy) (g *poolGroup, reasons map[int]leftOut) {
	reasons = map[int]leftOut{}

	var oriented []int

	for i := range items {
		it := &items[i]
		if !it.continuous && !it.fromCounts {
			continue
		}

		if scaleSign(it) == 0 {
			reasons[i] = *leftOutBecause(it.doi, leftScaleUnknown)

			continue
		}

		oriented = append(oriented, i)
	}

	g = &poolGroup{measure: model.MeasureSMD, converted: true}

	for _, i := range dropMinorMetric(items, oriented, reasons) {
		it := items[i] // a copy: it is put on the unified scale
		it.measure, it.study, it.unit = model.MeasureSMD, it.standardized(scaleSign(&it)), ""
		g.items, g.idx = append(g.items, it), append(g.idx, i)
	}

	return g, reasons
}

// dropMinorMetric keeps final values and change scores out of one SMD pool:
// when both occur among the continuous studies, the metric with more
// participants stays (final on a tie) and the other is recorded in reasons. A
// study with no stated metric, and a study from counts, does not take sides.
// It returns the positions that stay.
func dropMinorMetric(items []pooledStudy, pos []int, reasons map[int]leftOut) []int {
	participants := map[model.ScoreMetric]int{}
	seen := map[model.ScoreMetric]bool{}

	for _, i := range pos {
		if it := &items[i]; it.continuous && it.metric != "" {
			participants[it.metric] += it.n
			seen[it.metric] = true
		}
	}

	if !seen[model.MetricFinal] || !seen[model.MetricChange] {
		return pos
	}

	minor := model.MetricChange
	if participants[model.MetricChange] > participants[model.MetricFinal] {
		minor = model.MetricFinal
	}

	kept := make([]int, 0, len(pos))

	for _, i := range pos {
		if it := &items[i]; it.continuous && it.metric == minor {
			reasons[i] = *leftOutBecause(it.doi, leftMetricMix)

			continue
		}

		kept = append(kept, i)
	}

	return kept
}

// preferUnified decides between the best native pool and the unified SMD pool.
// Nothing changes when the best native pool already holds every study. Otherwise
// the unified pool wins when it holds at least two studies and MORE participants
// than the native one (the rule that picks among native pools, applied again: a
// larger body of data comes first); the reasons for the studies it leaves out
// come back with it. The native pool, and no reasons, otherwise.
func (p *Pipeline) preferUnified(
	items []pooledStudy, best *poolGroup,
) (chosen *poolGroup, reasons map[int]leftOut) {
	if len(best.idx) == len(items) {
		return best, nil
	}

	unified, excluded := unifiedGroup(items)
	if len(unified.items) < 2 || unified.participants() <= best.participants() {
		return best, nil
	}

	p.log.Info("one outcome on several measures: pooled as a standardized mean difference, "+
		"positive favours the intervention",
		"studies", len(unified.items), "participants", unified.participants(),
		"native_measure", best.measure, "native_studies", len(best.items), "native_participants", best.participants(),
		"excluded_by_scale", len(excluded))

	return unified, excluded
}
