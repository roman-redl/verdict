package pipeline

// Risk of bias per study, aggregated by weight in Go. The synthesis model used
// to answer each risk-of-bias item once for "the studies that carry most of the
// evidence", and which studies those were flipped between repeats. Now it
// answers for every direct primary study and the code weighs the answers: an
// item counts when the studies that answered yes carry more than half of the
// weight.
//
// The weights are the pool's (the same random-effects shares the lack-of-blinding
// rule uses; a trial reported several times has the weight of the report the pool
// kept) when there is a pooled estimate: only the pooled studies carry weight,
// the risk of bias of the evidence is the risk of the studies the estimate rests
// on. Without a pooled estimate every direct primary study is weighted by its
// known participants; a study that states no size weighs as much as the mean of
// the known sizes, and when no size is known all the studies weigh the same.

import (
	"github.com/roman-redl/verdict/internal/model"
)

// shareEpsilon absorbs floating-point noise in the shares: weights that add up
// to exactly one half must not count as more than a half.
const shareEpsilon = 1e-9

// riskItem is one per-study risk-of-bias item: the cause it is named by and how
// it is read from an answer. The order is the order of the causes in the report.
type riskItem struct {
	cause string
	get   func(*model.RiskOfBiasStudy) model.YesNo
}

var riskItems = []riskItem{
	{model.CauseAttrition, func(s *model.RiskOfBiasStudy) model.YesNo { return s.Attrition }},
	{model.CauseAllocationFlaw, func(s *model.RiskOfBiasStudy) model.YesNo { return s.AllocationFlaw }},
	{model.CauseSelectiveOutcome, func(s *model.RiskOfBiasStudy) model.YesNo { return s.SelectiveOutcome }},
	{model.CauseStoppedEarlyForBenefit, func(s *model.RiskOfBiasStudy) model.YesNo { return s.StoppedEarlyForBenefit }},
}

// riskStudy is a direct primary study as the aggregation sees it: every DOI it
// is reported under (the title for a report without one, answerKey) and its
// weight.
type riskStudy struct {
	dois   []string
	weight float64
}

// riskStudiesFor weighs the direct primary studies of the evidence: by the
// pool's weights when the estimate has them and a pooled study is among the
// studies, else by participants. byPool tells which one was used: it is false
// with an estimate that has weights only when they matched no study (a state
// the pool cannot produce), which the caller logs.
func riskStudiesFor(studies []evidenceStudy, pe *model.PooledEstimate) (out []riskStudy, byPool bool) {
	var primary []evidenceStudy

	for i := range studies {
		if !studies[i].design.IsSecondary() {
			primary = append(primary, studies[i])
		}
	}

	if poolWeights(pe) != nil {
		out = make([]riskStudy, len(primary))

		var total float64

		for i := range primary {
			out[i] = riskStudy{dois: primary[i].dois, weight: primary[i].weight}
			total += primary[i].weight
		}

		if total > 0 {
			return out, true
		}
	}

	return participantWeighted(primary), false
}

// riskWeights is the direct primary studies of the run with their weights (see
// riskStudiesFor); it logs the state the pool cannot produce.
func (p *Pipeline) riskWeights(st *State) []riskStudy {
	_, studies := evidenceStudies(st, st.Pooled)
	out, byPool := riskStudiesFor(studies, st.Pooled)

	if !byPool && poolWeights(st.Pooled) != nil {
		p.log.Warn("risk of bias weighted by participants: the pool weights match no direct primary study")
	}

	return out
}

// warnOnFallbacks logs the places where the model's reply did not have the
// expected shape and a default was taken: risk of bias read from the global
// answers (no per_study), per-study items read as "no" or entries left out,
// and the missing imprecision rating.
func (p *Pipeline) warnOnFallbacks(st *State, cl *model.GradeChecklist) {
	if cl.RiskOfBias.PerStudy == nil {
		p.log.Warn("risk of bias read from the global answers: the reply has no risk_of_bias.per_study")
	}

	if r := cl.RiskOfBias.Repairs; r.ItemsFilled > 0 || r.EntriesDropped > 0 {
		p.log.Warn("risk of bias per_study repaired: a missing item counts as no, an entry with an unknown answer "+
			"is left out (the study counts as no everywhere)",
			"items_filled", r.ItemsFilled, "entries_dropped", r.EntriesDropped)
	}

	if st.Imprecision == nil && cl.ImprecisionWithoutPool == nil {
		p.log.Warn("imprecision taken as 0: no computed imprecision and no imprecision_without_pool")
	}
}

// participantWeighted weighs each study by its known participants; an unknown
// size weighs as the mean of the known ones, all the same when none is known.
func participantWeighted(primary []evidenceStudy) []riskStudy {
	var sum float64

	known := 0

	for i := range primary {
		if primary[i].size > 0 {
			sum += float64(primary[i].size)
			known++
		}
	}

	unknown := 1.0
	if known > 0 {
		unknown = sum / float64(known)
	}

	out := make([]riskStudy, len(primary))

	for i := range primary {
		w := unknown
		if primary[i].size > 0 {
			w = float64(primary[i].size)
		}

		out[i] = riskStudy{dois: primary[i].dois, weight: w}
	}

	return out
}

// riskShares is, for each risk-of-bias item, the share of the weight carried by
// the studies that answered yes. An answer is matched to a study by any of the
// study's DOIs (the title for a report without one); an answer that matches no
// direct primary study is ignored, several
// answers for one study count as yes where any of them is, and a study without
// an answer counts as no everywhere. Every item is in the result.
func riskShares(studies []riskStudy, answers []model.RiskOfBiasStudy) map[string]float64 {
	yes := studyYes(studies, answers)

	var total float64

	yesWeight := map[string]float64{}

	for i := range studies {
		total += studies[i].weight

		for cause := range yes[i] {
			yesWeight[cause] += studies[i].weight
		}
	}

	shares := make(map[string]float64, len(riskItems))

	for _, it := range riskItems {
		shares[it.cause] = 0
		if total > 0 {
			shares[it.cause] = yesWeight[it.cause] / total
		}
	}

	return shares
}

// studyYes is, for each study, the set of items that some answer for it says
// yes to (matched by any of its DOIs, in the normalized form).
func studyYes(studies []riskStudy, answers []model.RiskOfBiasStudy) []map[string]bool {
	index := map[string]int{}

	for i := range studies {
		for _, d := range studies[i].dois {
			if _, dup := index[d]; !dup {
				index[d] = i
			}
		}
	}

	yes := make([]map[string]bool, len(studies))

	for i := range yes {
		yes[i] = map[string]bool{}
	}

	for _, a := range answers {
		at, ok := index[model.NormalizeDOI(a.DOI)]
		if !ok {
			continue
		}

		for _, it := range riskItems {
			if it.get(&a).IsYes() {
				yes[at][it.cause] = true
			}
		}
	}

	return yes
}

// globalAnswer is the global answers of an older reply as one study's answer.
func globalAnswer(it *model.RiskOfBiasItems) model.RiskOfBiasStudy {
	return model.RiskOfBiasStudy{
		Attrition: it.Attrition, AllocationFlaw: it.AllocationFlaw,
		SelectiveOutcome: it.SelectiveOutcome, StoppedEarlyForBenefit: it.StoppedEarlyForBenefit,
	}
}

// riskOfBiasDomain lowers one level for each distinct cause among the computed
// lack of blinding and the checklist items, at most two levels. An item counts
// when the weighted share of the studies answering yes is above one half; the
// shares are stored with the domain. A reply without per_study (an older one)
// gives one global answer per item, which counts when it is yes.
func riskOfBiasDomain(blinding bool, studies []riskStudy, it *model.RiskOfBiasItems) model.GradeDomain {
	var causes []string

	if blinding {
		causes = append(causes, model.CauseBlinding)
	}

	if it.PerStudy == nil {
		g := globalAnswer(it)

		for _, ri := range riskItems {
			if ri.get(&g).IsYes() {
				causes = append(causes, ri.cause)
			}
		}

		return model.GradeDomain{Levels: min(len(causes), maxRiskOfBiasLevels), Causes: causes}
	}

	shares := riskShares(studies, it.PerStudy)

	for _, ri := range riskItems {
		if shares[ri.cause] > majority+shareEpsilon {
			causes = append(causes, ri.cause)
		}
	}

	return model.GradeDomain{Levels: min(len(causes), maxRiskOfBiasLevels), Causes: causes, Shares: shares}
}
