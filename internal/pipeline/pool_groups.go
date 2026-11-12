package pipeline

// The candidate pools the eligible studies could form, and how they are
// ranked: one pool per native measure (pool.go chooses among them) and, beside
// them, the unified SMD pool (pool_unify.go).

import (
	"slices"

	"github.com/roman-redl/verdict/internal/meta"
	"github.com/roman-redl/verdict/internal/model"
)

// poolGroup is a candidate pool: the studies that can be expressed on one
// measure (and, for MD, one unit). Its items are copies already put on the
// group's measure; idx are the studies' positions in the poolBest input.
// converted marks the unified SMD pool, built from studies on different
// measures and oriented so that positive favours the intervention.
type poolGroup struct {
	measure   model.Measure
	unit      string
	converted bool
	items     []pooledStudy
	idx       []int
}

func (g *poolGroup) participants() int {
	total := 0

	for i := range g.items {
		total += g.items[i].n
	}

	return total
}

// better orders groups by the amount of data: more participants (a study of
// unknown size counts 0), then more studies, then the fixed measure order, then
// the unit (so that the choice is deterministic).
func (g *poolGroup) better(o *poolGroup) bool {
	if a, b := g.participants(), o.participants(); a != b {
		return a > b
	}

	if len(g.items) != len(o.items) {
		return len(g.items) > len(o.items)
	}

	if a, b := slices.Index(measureOrder, g.measure), slices.Index(measureOrder, o.measure); a != b {
		return a < b
	}

	return normUnit(g.unit) < normUnit(o.unit)
}

// candidateGroups builds the pools the studies could form, one per measure
// (and, for MD, per unit). Ratio measures are never mixed with each other,
// except that a study from counts is eligible for both RR and OR (it has both
// on its analysis scale); HR stays apart (time-to-event), an MD pools only
// within one unit. A study from counts is therefore in two candidates, but
// only one candidate is ever pooled.
func candidateGroups(items []pooledStudy) []*poolGroup {
	var groups []*poolGroup

	add := func(i int, measure model.Measure, study meta.Study) {
		it := items[i] // a copy: it is put on this group's measure
		it.measure, it.study = measure, study

		gi := slices.IndexFunc(groups, func(g *poolGroup) bool {
			return g.measure == measure && normUnit(g.unit) == normUnit(it.unit)
		})
		if gi < 0 {
			groups = append(groups, &poolGroup{measure: measure, unit: it.unit})
			gi = len(groups) - 1
		}

		groups[gi].items = append(groups[gi].items, it)
		groups[gi].idx = append(groups[gi].idx, i)
	}

	for i := range items {
		add(i, items[i].measure, items[i].study)

		if items[i].fromCounts {
			add(i, model.MeasureOR, items[i].or)
		}
	}

	return groups
}
