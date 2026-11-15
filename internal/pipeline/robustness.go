package pipeline

// The leave-one-out check of the pooled conclusion. The critic's labels vary
// between runs, and on thin evidence the conclusion can turn on one study
// entering or leaving the pool. The check shows it: the chosen pool is made
// again without each study in turn, by the same arithmetic on the same
// measure (no new choice of group or measure), and the effect direction is
// derived from the result as it is for the whole pool. A study is decisive when
// its absence changes the direction. It is information for the reader: it
// feeds neither the GRADE level nor the synthesis.

import (
	"slices"

	"github.com/roman-redl/verdict/internal/model"
)

// impliedDirection is the effect direction a target implies for the claim; ok
// is false when the target needs the claim's change and there is none.
func impliedDirection(target model.ImprecisionTarget, claim model.ClaimChange) (model.EffectDirection, bool) {
	if target.DependsOnClaim() && claim == "" {
		return "", false
	}

	return target.DirectionFor(claim)
}

// assessRobustness leaves each study of the pool out in turn. It returns nil
// without a pooled estimate (the evidence is judged study by study) and, with a
// warning, when a direction cannot be derived for the whole pool or for one of
// the smaller ones: the report then makes no claim about robustness. A pool of
// one study rests on it by definition.
func (p *Pipeline) assessRobustness(
	res poolResult, imp *model.Imprecision, claimChange string,
) *model.Robustness {
	g, full := res.group, res.est
	if g == nil || full == nil {
		return nil
	}

	k := len(g.items)
	if k == 1 {
		return &model.Robustness{Checked: 1, SingleStudy: true}
	}

	if imp == nil {
		p.log.Warn("robustness not computed: the pooled estimate has no imprecision rating")

		return nil
	}

	if len(full.Weights) != k {
		p.log.Error("robustness not computed: pool weights do not match the studies",
			"studies", k, "weights", len(full.Weights))

		return nil
	}

	// a missing or invalid claim_change leaves claim empty: only the targets that
	// need it have no direction then
	claim, claimErr := model.ParseClaimChange(claimChange)

	want, ok := impliedDirection(imp.Target, claim)
	if !ok {
		p.log.Warn("robustness not computed: no direction for the whole pool", "target", imp.Target, "err", claimErr)

		return nil
	}

	r := &model.Robustness{Checked: k, Direction: want}

	for i := range g.items {
		without, unstated, ok := p.directionWithout(g, full, i, claim)
		if !ok {
			return nil
		}

		// a direction that cannot be stated for want of a claim_change is still a
		// change from the whole pool's one, which needed none
		if without == want && !unstated {
			continue
		}

		it := &g.items[i]
		r.Decisive = append(r.Decisive, model.DecisiveStudy{
			Study: it.doi, Title: it.title, Participants: it.listSize, Weight: full.Weights[i], DirectionWithout: without,
		})
	}

	return r
}

// directionWithout is the effect direction of the pool g without its study i:
// the same measure and the same arithmetic, then the imprecision rule and the
// direction as for the whole pool (full). The mean-difference band is the whole
// pool's: it has one only when the whole pool does, so that a smaller pool does
// not gain a band of its own and only the data change. unstated is true when
// the interval excludes no effect but the direction against the claim cannot be
// told without a claim_change: the direction is then "unclear". ok is false
// when the smaller pool cannot be pooled or rated (each is logged).
func (p *Pipeline) directionWithout(
	g *poolGroup, full *model.PooledEstimate, i int, claim model.ClaimChange,
) (direction model.EffectDirection, unstated, ok bool) {
	study := g.items[i].doi

	rest := &poolGroup{
		measure: g.measure, unit: g.unit, converted: g.converted,
		items: slices.Delete(slices.Clone(g.items), i, i+1),
	}

	pe, _, err := pooledFrom(rest)
	if err != nil {
		p.log.Warn("robustness not computed: the pool without a study cannot be pooled", "study", study, "err", err)

		return "", false, false
	}

	if full.SD == nil {
		pe.SD = nil
	}

	imp, err := rateImprecision(pe)
	if err != nil {
		p.log.Warn("robustness not computed: the pool without a study cannot be rated", "study", study, "err", err)

		return "", false, false
	}

	if imp.Target.DependsOnClaim() && claim == "" {
		return model.EffectUnclear, true, true
	}

	d, ok := impliedDirection(imp.Target, claim)
	if !ok {
		p.log.Warn("robustness not computed: no direction for the pool without a study", "study", study, "target", imp.Target)
	}

	return d, false, ok
}
