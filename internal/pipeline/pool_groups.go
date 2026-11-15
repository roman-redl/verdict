package pipeline

// The candidate pools the eligible studies could form, and how they are
// ranked: one pool per native measure (pool.go chooses among them) and, beside
// them, the unified SMD pool (pool_unify.go).

import (
	"cmp"
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

// sameAmount is the participants and the number of the studies of the question's
// own outcome (not labelled related); a related study adds to a pool but never
// decides which pool is chosen.
func (g *poolGroup) sameAmount() (participants, studies int) {
	for i := range g.items {
		if !g.items[i].related {
			participants += g.items[i].n
			studies++
		}
	}

	return participants, studies
}

// hasSameOutcome is true when the group holds a study of the question's own
// outcome (labelled same, or not labelled).
func (g *poolGroup) hasSameOutcome() bool {
	return slices.ContainsFunc(g.items, func(it pooledStudy) bool { return !it.related })
}

// moreParticipants is true when g has strictly more participants than o, the
// comparison that lets the unified pool replace a native one (the number of
// studies plays no part, a tie keeps the native pool). A group with a study of
// the question's own outcome beats one without; between groups that both have
// such studies only their participants count, between groups of related studies
// alone all of them. Without related studies it is the plain count of
// participants.
func (g *poolGroup) moreParticipants(o *poolGroup) bool {
	if a, b := g.hasSameOutcome(), o.hasSameOutcome(); a != b {
		return a
	}

	if !g.hasSameOutcome() {
		return g.participants() > o.participants()
	}

	gPeople, _ := g.sameAmount()
	oPeople, _ := o.sameAmount()

	return gPeople > oPeople
}

// moreData compares the amount of data of two groups: positive when g has more.
// A group with a study of the question's own outcome always has more than one
// without (a pool of related outcomes alone is only the last resort). Between
// groups that both have such studies only those studies count: their
// participants, then their number (a study of unknown size counts 0
// participants), so that a related study never decides; between groups of
// related studies alone all of them count the same way. Without related studies
// this is the plain count of participants, then of studies.
func (g *poolGroup) moreData(o *poolGroup) int {
	if a, b := g.hasSameOutcome(), o.hasSameOutcome(); a != b {
		if a {
			return 1
		}

		return -1
	}

	gPeople, gStudies := g.sameAmount()
	oPeople, oStudies := o.sameAmount()

	if !g.hasSameOutcome() {
		gPeople, gStudies, oPeople, oStudies = g.participants(), len(g.items), o.participants(), len(o.items)
	}

	if c := cmp.Compare(gPeople, oPeople); c != 0 {
		return c
	}

	return cmp.Compare(gStudies, oStudies)
}

// better orders groups by the amount of data (moreData, the number of studies
// included), then by the fixed measure order, then by the unit (so that the
// choice is deterministic).
func (g *poolGroup) better(o *poolGroup) bool {
	if c := g.moreData(o); c != 0 {
		return c > 0
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
