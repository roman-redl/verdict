package pipeline

// What the pool needs from the critic beyond the effect itself: the number of
// events behind a ratio (shown in the pooled line and the evidence base; the
// imprecision rule does not read it) and the blinding of the trial with the
// objectivity of its outcome (the risk-of-bias rule for lack of blinding). Both
// are optional in the critique: a missing value is unknown, and an unknown
// value never makes a rule fire.

import (
	"fmt"
	"math"

	"github.com/roman-redl/verdict/internal/model"
)

// blindingLevels is the rating of the lack-of-blinding rule: always serious
// (one level); the synthesis may still add a second one for other flaws.
const blindingLevels = 1

// majority is the share of the pool's weight that counts as "most".
const majority = 0.5

// totalEvents is the events of a study that gives no arm counts: the total the
// critic copied (Effect.EventsTotal), as reported. A study without arm counts
// gives a reported estimate, and clusterAdjusted leaves such an estimate and its
// participants as reported, even for a cluster trial; the events are taken the
// same way, so that they stay on the scale of the participants beside them.
func totalEvents(ef *model.Effect) (events float64, ok bool) {
	if !positiveNumber(ef.EventsTotal) {
		return 0, false
	}

	return *ef.EventsTotal, true
}

// withSource notes on the pooled study what the critique says beyond the
// numbers: the events, and the blinding and the outcome's objectivity for the
// blinding rule. The events are the arms' counts when the study gave them
// (deriveCounts, already effective for a cluster trial), else the critic's
// total as reported. Blinding of a study whose design is known not to be
// randomized is not kept (the critic is told to leave it out), so that a stray
// value cannot make the rule fire.
func (ps *pooledStudy) withSource(c model.Critique, ef *model.Effect, design model.StudyDesign) {
	if !ps.eventsKnown {
		ps.events, ps.eventsKnown = totalEvents(ef)
	}

	ps.objective = ef.OutcomeObjective

	if !design.IsKnown() || design.IsRandomized() {
		ps.blinding = c.Blinding
	}
}

// events is the total number of events of the pool, rounded; zero (unknown)
// when any study does not give its count.
func (g *poolGroup) events() int {
	var total float64

	for i := range g.items {
		if !g.items[i].eventsKnown {
			return 0
		}

		total += g.items[i].events
	}

	return int(math.Round(total))
}

// blindingBias applies the lack-of-blinding rule to the studies of a pool with
// their weights in it: it fires when studies that are open-label AND have a
// subjective or self-reported outcome (objective explicitly false) carry more
// than half of the weight. An unclear or missing blinding counts as not open,
// an unknown objectivity as not subjective. Nil when it does not fire. There
// is one weight per study (ratedBlinding checks it).
func blindingBias(items []pooledStudy, weights []float64) *model.BlindingBias {
	// only a study that is both open-label and measures a subjective outcome
	// counts: an open trial of death or a blinded trial of pain carries no
	// such bias
	var openSubjective float64

	for i := range items {
		if items[i].blinding == model.BlindingOpen && items[i].objective != nil && !*items[i].objective {
			openSubjective += weights[i]
		}
	}

	if openSubjective <= majority {
		return nil
	}

	return &model.BlindingBias{
		Levels: blindingLevels,
		Reason: fmt.Sprintf("open-label studies with a subjective or self-reported outcome carry %.0f%% "+
			"of the pool weight", openSubjective*100),
	}
}

// ratedBlinding is blindingBias for the studies of a pool. The arithmetic
// returns one weight per study, so a mismatch is a bug, not a data problem:
// it is logged at Error and the pool goes on without the rating.
func (p *Pipeline) ratedBlinding(items []pooledStudy, weights []float64) *model.BlindingBias {
	if len(items) != len(weights) {
		p.log.Error("blinding rule skipped: pool weights do not match the studies",
			"studies", len(items), "weights", len(weights))

		return nil
	}

	return blindingBias(items, weights)
}
