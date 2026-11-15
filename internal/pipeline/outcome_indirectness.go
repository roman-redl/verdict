package pipeline

// The outcome part of the indirectness domain, computed from the pool. The pool
// takes studies of the question's outcome and of a related one (a surrogate, a
// composite, a component, another definition); how far the evidence rests on
// related outcomes is the share of the pool's weight they carry, and when that
// share is above one half the outcome lowers the indirectness, the same
// weighted-majority rule as the risk-of-bias items (grade_risk.go). The critic's
// borderline choice between "same" and "related" then moves the share, not the
// existence of the pool.

import (
	"github.com/roman-redl/verdict/internal/model"
)

// relatedOutcomes is the share of the pool's weight carried by studies of a
// related outcome. It is nil when there is no pooled estimate with weights (the
// outcome is then judged by the model alone): the weights are the ones the
// lack-of-blinding and risk-of-bias rules read (riskStudiesFor).
func relatedOutcomes(studies []evidenceStudy, pe *model.PooledEstimate) *model.RelatedOutcomes {
	weighted, byPool := riskStudiesFor(studies, pe)
	if !byPool {
		return nil
	}

	var total, related float64

	for i := range weighted {
		total += weighted[i].weight
		related += weighted[i].related
	}

	if total <= 0 {
		return nil
	}

	share := related / total

	return &model.RelatedOutcomes{Share: share, RatedDown: share > majority+shareEpsilon}
}

// assessRelatedOutcomes computes the outcome indirectness of the pooled
// estimate of the run; see relatedOutcomes.
func (p *Pipeline) assessRelatedOutcomes(st *State) *model.RelatedOutcomes {
	_, studies := evidenceStudies(st, st.Pooled)

	ro := relatedOutcomes(studies, st.Pooled)
	if ro != nil && ro.Share > 0 {
		p.log.Info("pool holds related outcomes", "share", ro.Share, "rated_down", ro.RatedDown)
	}

	return ro
}
