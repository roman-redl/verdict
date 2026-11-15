package pipeline

// What the pool needs from the critic beyond the effect itself: the number of
// events behind a ratio (shown in the pooled line and the evidence base; the
// imprecision rule does not read it) and the blinding of the trial with the
// objectivity and self-reporting of its outcome (the risk-of-bias rule for lack
// of blinding). Both
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

// byParticipantsBasis names the weights of the rule without a pooled estimate
// in its reason.
const byParticipantsBasis = "of the participants (weighted by participants: there is no pooled estimate)"

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
// numbers: the events, and the blinding and the outcome's objectivity and
// self-reporting for the blinding rule. The events are the arms' counts when the study gave them
// (deriveCounts, already effective for a cluster trial), else the critic's
// total as reported. Blinding of a study whose design is known not to be
// randomized is not kept (the critic is told to leave it out), so that a stray
// value cannot make the rule fire.
func (ps *pooledStudy) withSource(c model.Critique, ef *model.Effect, design model.StudyDesign) {
	if !ps.eventsKnown {
		ps.events, ps.eventsKnown = totalEvents(ef)
	}

	ps.objective = ef.OutcomeObjective
	ps.selfReported = ef.OutcomeSelfReported

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
// their weights in it: it fires when the studies that count as unblinded
// (unblindedBias) carry more than half of the weight. Nil when it does not
// fire. There is one weight per study (ratedBlinding checks it).
func blindingBias(items []pooledStudy, weights []float64) *model.BlindingBias {
	var unblinded float64

	for i := range items {
		if unblindedBias(&items[i]) {
			unblinded += weights[i]
		}
	}

	if unblinded <= majority {
		return nil
	}

	return &model.BlindingBias{Levels: blindingLevels, Reason: blindingReason(unblinded, "of the pool weight")}
}

// blindingRating is the lack-of-blinding rating of the run: the pool's own
// (res.blinding, by the pool weights) when there is a pooled estimate, else the
// rule over the randomized trials weighted by participants.
func blindingRating(st *State, res poolResult) *model.BlindingBias {
	if res.est != nil {
		return res.blinding
	}

	_, studies := evidenceStudies(st, nil)

	return blindingBiasWithoutPool(studies)
}

// blindingReason is the sentence behind the rating: what counts as unblinded,
// and the share of the weight those studies carry, with the weight's basis.
func blindingReason(share float64, basis string) string {
	return fmt.Sprintf("studies with a subjective outcome and unmasked participants (open-label), or "+
		"with an outcome the participants report themselves and only the outcome assessor masked, "+
		"carry %.0f%% %s", share*100, basis)
}

// blindingBiasWithoutPool is the lack-of-blinding rule for a corpus without a
// pooled estimate. The studies are the direct primary randomized trials of the
// run, one per trial (evidenceStudies); blinding is defined for randomized
// trials only, so the other designs are not in the denominator. Each weighs by
// its participants (participantWeighted, as the risk-of-bias items do without a
// pool); the labels are the critic's, and a trial without an effect has unknown
// outcome labels, so it does not count as unblinded. Nil when the rule does not
// fire.
func blindingBiasWithoutPool(studies []evidenceStudy) *model.BlindingBias {
	var randomized []evidenceStudy

	for i := range studies {
		if studies[i].design.IsRandomized() {
			randomized = append(randomized, studies[i])
		}
	}

	var total, unblinded float64

	for i, w := range participantWeighted(randomized) {
		total += w.weight

		if s := &randomized[i]; unblindedLabels(s.blinding, s.objective, s.selfReported) {
			unblinded += w.weight
		}
	}

	if total <= 0 || unblinded/total <= majority+shareEpsilon {
		return nil
	}

	return &model.BlindingBias{Levels: blindingLevels, Reason: blindingReason(unblinded/total, byParticipantsBasis)}
}

// unblindedBias reports whether a study carries the bias of lack of blinding.
// An open trial counts when its outcome is subjective (objective explicitly
// false) or reported by the participants themselves: an open trial of death
// carries no such bias. A trial with only the outcome assessor masked counts
// only when the participants report the outcome themselves (pain, quality of
// life, a diary): a masked assessor of an observer-rated outcome (a clinician's
// scale) is low risk of bias, so it does not count; an unknown self-reporting
// does not count either. An outcome stated objective never counts, even when
// the participants read it themselves (a home scale, a worn device). Single-blind
// (participants masked), double-blind, unclear and missing blinding never count,
// the rule does not fire on a guess.
func unblindedBias(s *pooledStudy) bool {
	return unblindedLabels(s.blinding, s.objective, s.selfReported)
}

// unblindedLabels is unblindedBias on the critic's labels themselves, for the
// studies that are not pooled (nil is unknown).
func unblindedLabels(blinding model.Blinding, objectiveLabel, selfReportedLabel *bool) bool {
	objective := objectiveLabel != nil && *objectiveLabel
	subjective := objectiveLabel != nil && !*objectiveLabel
	selfReported := selfReportedLabel != nil && *selfReportedLabel

	switch blinding {
	case model.BlindingOpen:
		return subjective || (selfReported && !objective)
	case model.BlindingAssessorOnly:
		return selfReported && !objective
	case model.BlindingDouble, model.BlindingSingle, model.BlindingUnclear:
		return false
	}

	return false
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
