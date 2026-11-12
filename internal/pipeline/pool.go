package pipeline

// Pooling the effects of the direct studies, as a meta-analysis does. The
// critic only EXTRACTS the numbers a study reports (model.Effect), Go COMPUTES
// the pooled estimate (internal/meta), and the synthesis READS the result: the
// model never does arithmetic, and a verdict is not left to "each study is
// non-significant" when the pooled interval excludes no effect.

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/roman-redl/verdict/internal/meta"
	"github.com/roman-redl/verdict/internal/model"
)

// measureOrder breaks ties between pools of equal size: a count-based RR
// first, then the other ratios, then the continuous measures.
var measureOrder = []model.Measure{
	model.MeasureRR, model.MeasureOR, model.MeasureHR, model.MeasureSMD, model.MeasureMD,
}

// pooledStudy is one direct study's effect on the analysis scale.
type pooledStudy struct {
	doi     string
	measure model.Measure
	unit    string // of an MD
	study   meta.Study
	n       int // participants in the effect's arms; 0 when unknown

	// Counts give an odds ratio as well as a risk ratio: a study from counts
	// carries its RR in study (measure RR, its default) and the OR here, so that
	// it can join whichever ratio pool is chosen.
	fromCounts bool
	or         meta.Study

	// converted says which counts Go derived from the arms' percentages
	// ("" when none), so that the conversion is visible in the log.
	converted string

	// flipped describes a reported estimate turned from control-vs-intervention
	// into intervention-vs-control ("" when it was not reversed).
	flipped string

	// Raw continuous data waits until the pool knows whether every such study
	// shares one unit (then MD) or not (then SMD).
	continuous bool
	md, smd    meta.Study

	// sd is the SD of the outcome across both arms of a study derived from
	// means; 0 when unknown (a reported estimate carries none).
	sd float64

	// The critic's labels of the outcome's direction (nil or empty: unknown),
	// the input of the unified SMD pool: higher-is-better and the metric of a
	// continuous outcome, whether the counted event is a harm for a binary one.
	higherIsBetter *bool
	eventIsHarm    *bool
	metric         model.ScoreMetric

	// outcome is the critic's name of the outcome with its time point.
	outcome string

	// trial is 1 + the index of the first paper of the study's trial (papers
	// that share a trial registration number report one trial); 0 means it is
	// not known to repeat another study. regs are the study's registration
	// numbers, year its publication year and rank how well it answers the
	// question: the reasons to keep one report of a trial.
	trial int
	regs  []string
	year  int
	rank  reportRank

	// results is the study's extracted arm data as one text (resultsKey), the
	// key of the exact-duplicate check; empty without arm data.
	results string
}

var errNoNumbers = errors.New("no usable numbers")

// poolResult is what pooling made of the corpus: the pooled estimate, nil when
// no study with usable numbers was left, and every study that was kept out with
// the reason (the estimate's left_out lists them; without an estimate they are
// only logged and warned about).
type poolResult struct {
	est     *model.PooledEstimate
	leftOut []leftOut
}

// poolEffects is the pooled estimate of the corpus; see pool.
func (p *Pipeline) poolEffects(st *State) *model.PooledEstimate { return p.pool(st).est }

// pool pools the effects the critics extracted from the direct, non-retracted
// primary studies. Reviews and meta-analyses, and papers that are not study
// reports, are left out. The order of the rules is fixed: the design stratum,
// the outcome and the time point, the cluster adjustment; then one report per
// trial; then exact duplicates; then the measure. Numbers that cannot be an
// effect are ignored with a warning.
func (p *Pipeline) pool(st *State) poolResult {
	crits := critiquesFor(st)
	trials := trialGroups(trialInputs(st, crits))
	stratum := poolStratum(st, crits)

	var (
		items []pooledStudy
		out   []leftOut
	)

	for i := range st.Enriched {
		e := &st.Enriched[i]

		ps, left, ok := p.studyEffect(e, crits[i], stratum)
		if left != nil {
			out = append(out, *left)
		}

		if !ok {
			continue
		}

		ps.trial, ps.regs, ps.year = trials[i]+1, e.TrialIDs(), e.Year
		ps.rank = rankOf(ps.doi, ps.year, crits[i].Effect)
		items = append(items, ps)
	}

	items, dropped := chooseReports(items)
	out = append(out, dropped...)

	items, dropped = dropDuplicates(items)
	out = append(out, dropped...)

	var est *model.PooledEstimate

	if len(items) > 0 {
		var measureLeft []leftOut

		est, measureLeft = p.poolBest(settleContinuous(items))
		out = append(out, measureLeft...)
	}

	p.logLeftOut(out)

	if est != nil {
		est.Stratum, est.LeftOut = stratum, leftOutTexts(out)
	}

	return poolResult{est: est, leftOut: out}
}

// studyEffect puts one study's effect on its analysis scale. ok is false when
// the study does not take part in pooling; left is set when it would have, but
// the rules keep it out (and ok is false then too).
func (p *Pipeline) studyEffect(
	e *model.EnrichedPaper, c model.Critique, stratum model.Stratum,
) (ps pooledStudy, left *leftOut, ok bool) {
	if c.Effect == nil || c.Relevance != relevanceDirect || e.Flags.Retracted {
		return pooledStudy{}, nil, false
	}

	doi := cmp.Or(e.DOI, e.Title)
	design := resolveDesign(e, c)

	// A review's estimate repeats the data of the trials it includes, and
	// pooling both counts that data twice (a falsely narrow interval). A
	// study of unknown design is not dropped for that: dropping it on a guess
	// would lose a real trial.
	if design.IsSecondary() {
		p.log.Info("effect not pooled: secondary research", "doi", doi, "design", design)
		return pooledStudy{}, nil, false
	}

	// an erratum or a comment carries no results of its own
	if kind := model.NonStudyKind(e.PublicationTypes); kind != "" {
		p.log.Info("effect not pooled: not a study report", "doi", doi, "kind", kind)
		return pooledStudy{}, nil, false
	}

	// an SD the paper gives as an SE or an interval of the arm's mean is
	// restored before anything else reads the arms (the cluster adjustment
	// changes n, the SD comes from the n the paper reports)
	ef := p.restoreSDs(doi, c.Effect)

	ps, err := deriveEffect(ef)
	if err != nil {
		p.log.Warn("effect not pooled: unusable numbers", "doi", e.DOI, "err", err)
		return pooledStudy{}, nil, false
	}

	if kind := eligibilityKind(design, c.Effect, stratum); kind != "" {
		return pooledStudy{}, leftOutBecause(doi, kind), false
	}

	if ps, left, ok = p.adjustCluster(doi, design, ef, ps); !ok {
		return pooledStudy{}, left, false
	}

	// the key is the numbers as extracted (SDs restored), not as adjusted
	ps.doi, ps.results = doi, resultsKey(ef)
	ps.outcome = outcomeLabel(c.Effect)

	if ps.converted != "" {
		p.log.Info("effect counts derived from percentages", "doi", e.DOI, "derived", ps.converted)
	}

	if ps.flipped != "" {
		p.log.Info("reported estimate reversed to intervention vs control", "doi", e.DOI, "flipped", ps.flipped)
	}

	if ps.n == 0 {
		ps.n = cmp.Or(e.SampleSize, c.SampleSizeReported)
	}

	return ps, nil, true
}

// adjustCluster puts a cluster trial's counts or means on the scale of the
// effective sample (clusterAdjusted); the trial is left out when that cannot be
// done. Another study passes unchanged.
func (p *Pipeline) adjustCluster(
	doi string, design model.StudyDesign, ef *model.Effect, ps pooledStudy,
) (pooledStudy, *leftOut, bool) {
	adjusted, de, icc, ok := clusterAdjusted(design, ef, &ps)
	if !ok {
		return ps, leftOutBecause(doi, leftCluster), false
	}

	if adjusted == ef {
		return ps, nil, true
	}

	eff, err := deriveEffect(adjusted)
	if err != nil {
		p.log.Warn("effect not pooled: unusable numbers after the cluster adjustment", "doi", doi, "err", err)
		return ps, nil, false
	}

	p.log.Info("cluster trial: participants divided by the design effect",
		"doi", doi, "design_effect", de, "icc", icc, "participants", ps.n, "effective", eff.n)

	return eff, nil, true
}

// tidyUnit collapses the spaces of a unit as the critic wrote it; normUnit is
// the key units are compared by (case and spacing do not make a new unit).
func tidyUnit(s string) string { return strings.Join(strings.Fields(s), " ") }

func normUnit(s string) string { return strings.ToLower(tidyUnit(s)) }

// settleContinuous decides, for the studies that gave means, between the mean
// difference (every one of them states the same unit) and the standardized
// one (units differ or are missing).
func settleContinuous(items []pooledStudy) []pooledStudy {
	unit, same := "", true

	for i := range items {
		if !items[i].continuous {
			continue
		}

		if unit == "" {
			unit = items[i].unit
		}

		same = same && items[i].unit != "" && normUnit(items[i].unit) == normUnit(unit)
	}

	for i := range items {
		it := &items[i]
		if !it.continuous {
			continue
		}

		if same {
			it.measure, it.study = model.MeasureMD, it.md
		} else {
			it.measure, it.study, it.unit = model.MeasureSMD, it.smd, ""
		}
	}

	return items
}

// poolBest takes the candidate with the most data and pools it: random effects
// for two or more studies, the study's own effect for one. The studies it gets
// are already eligible and one per trial, so the measure is the only choice
// left. The candidate with the most participants wins, so that a pair of pilot
// trials does not beat one large trial that happens to be on another measure;
// ties go by the number of studies, then measureOrder (RR before OR for
// counts), then the unit. The studies outside the winner come back as left out
// for their measure. When some studies stay outside the winner, the unified SMD
// pool (pool_unify.go) replaces it if it holds more participants.
func (p *Pipeline) poolBest(items []pooledStudy) (*model.PooledEstimate, []leftOut) {
	groups := candidateGroups(items)
	best := groups[0]

	for _, g := range groups[1:] {
		if g.better(best) {
			best = g
		}
	}

	best, reasons := p.preferUnified(items, best)

	pe, err := pooledFrom(best)
	if err != nil {
		p.log.Warn("effects not pooled", "measure", best.measure, "err", err)
		return nil, nil
	}

	var left []leftOut

	for i := range items {
		if slices.Contains(best.idx, i) {
			continue
		}

		if reason, ok := reasons[i]; ok {
			left = append(left, reason)

			continue
		}

		// a count study is labelled with its default measure, RR, though it
		// could have joined an OR pool
		left = append(left, leftOutMeasure(&items[i]))
	}

	if len(left) > 0 && !best.converted {
		p.log.Info("effects use several measures: pooling the group with most participants",
			"measure", best.measure, "studies", len(best.items), "left_out", len(left))
	}

	return pe, left
}

// pooledFrom runs the arithmetic and brings the result back to the natural
// scale (ratios are pooled as logs).
func pooledFrom(g *poolGroup) (*model.PooledEstimate, error) {
	studies := make([]meta.Study, len(g.items))
	dois := make([]string, len(g.items))

	for i := range g.items {
		studies[i], dois[i] = g.items[i].study, g.items[i].doi
	}

	var (
		res meta.Result
		err error
	)

	method := model.PoolRandomEffects
	if len(studies) == 1 {
		method = model.PoolSingleStudy
		res, err = meta.Single(studies[0])
	} else {
		res, err = meta.Pool(studies)
	}

	if err != nil {
		return nil, fmt.Errorf("pooling: %w", err)
	}

	back := func(v float64) float64 { return v }
	if g.measure.IsRatio() {
		back = math.Exp
	}

	pe := &model.PooledEstimate{
		Method: method, Measure: g.measure, Unit: g.unit,
		Estimate: back(res.Estimate), CILow: back(res.CILow), CIHigh: back(res.CIHigh),
		K: res.K, Studies: dois, Outcome: joinOutcomes(g.items),
	}

	if method == model.PoolRandomEffects {
		i2 := res.I2
		pe.I2 = &i2
	}

	pe.Participants = g.participants()
	for i := range g.items {
		if g.items[i].n == 0 { // one unknown size makes the total meaningless
			pe.Participants = 0
			break
		}
	}

	if g.measure == model.MeasureMD {
		pe.SD = g.meanSD()
	}

	if g.converted {
		pe.Converted, pe.Outcome = true, standardizedOutcome(pe.Outcome)
	}

	return pe, nil
}

// meanSD is the participant-weighted mean of the studies' SDs, the scale the
// imprecision band of an MD is taken from. It is nil unless every study knows
// its SD and size: a mean over some studies would describe another outcome
// scale than the pooled one.
func (g *poolGroup) meanSD() *float64 {
	var sum, weight float64

	for i := range g.items {
		it := &g.items[i]
		if it.sd <= 0 || it.n <= 0 {
			return nil
		}

		sum += it.sd * float64(it.n)
		weight += float64(it.n)
	}

	mean := sum / weight

	return &mean
}
