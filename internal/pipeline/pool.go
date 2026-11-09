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

// measureOrder breaks ties between equally large pools: a count-based RR
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

	// outcome is the critic's name of the outcome with its time point.
	outcome string
}

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

var errNoNumbers = errors.New("no usable numbers")

// poolEffects pools the effects the critics extracted from the direct,
// non-retracted studies. It returns nil when no study has usable numbers.
// Numbers that cannot be an effect are ignored with a warning.
func (p *Pipeline) poolEffects(st *State) *model.PooledEstimate {
	crits := critiquesFor(st)

	var items []pooledStudy

	for i := range st.Enriched {
		e, c := &st.Enriched[i], crits[i]
		if c.Effect == nil || c.Relevance != relevanceDirect || e.Flags.Retracted {
			continue
		}

		ps, err := deriveEffect(c.Effect)
		if err != nil {
			p.log.Warn("effect not pooled: unusable numbers", "doi", e.DOI, "err", err)
			continue
		}

		ps.doi = cmp.Or(e.DOI, e.Title)
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

		items = append(items, ps)
	}

	if len(items) == 0 {
		return nil
	}

	return p.poolBest(settleContinuous(items))
}

// deriveEffect puts one study's extracted numbers on an analysis scale:
// counts first, then means, then a reported estimate with its interval.
func deriveEffect(ef *model.Effect) (pooledStudy, error) {
	if ef.Intervention != nil && ef.Control != nil {
		a, b, ok, err := countArms(ef.Intervention, ef.Control)
		if err != nil {
			return pooledStudy{}, fmt.Errorf("counts: %w", err)
		}

		if ok {
			s, err := meta.LogRR(a.events, a.total, b.events, b.total)
			if err != nil {
				return pooledStudy{}, fmt.Errorf("counts: %w", err)
			}

			return pooledStudy{
				measure: model.MeasureRR, study: s, n: int(a.total + b.total),
				converted: strings.Join(slices.DeleteFunc([]string{a.derived, b.derived},
					func(s string) bool { return s == "" }), "; "),
			}, nil
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
		continuous: true, unit: tidyUnit(unit), md: md, smd: smd, sd: sd, n: int(a.N + b.N),
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

// poolGroup is the studies sharing one measure (and, for MD, one unit).
type poolGroup struct {
	measure model.Measure
	unit    string
	items   []pooledStudy
}

func (g *poolGroup) participants() int {
	total := 0

	for i := range g.items {
		total += g.items[i].n
	}

	return total
}

// better orders groups: more studies, then more participants, then the fixed
// measure order, then the unit (so that the choice is deterministic).
func (g *poolGroup) better(o *poolGroup) bool {
	if len(g.items) != len(o.items) {
		return len(g.items) > len(o.items)
	}

	if a, b := g.participants(), o.participants(); a != b {
		return a > b
	}

	if a, b := slices.Index(measureOrder, g.measure), slices.Index(measureOrder, o.measure); a != b {
		return a < b
	}

	return normUnit(g.unit) < normUnit(o.unit)
}

// poolBest groups the studies by measure (ratio measures are never mixed, and
// an MD only pools within one unit), takes the largest group and pools it:
// random effects for two or more studies, the study's own effect for one.
func (p *Pipeline) poolBest(items []pooledStudy) *model.PooledEstimate {
	var groups []*poolGroup

	for i := range items {
		it := &items[i]

		idx := slices.IndexFunc(groups, func(g *poolGroup) bool {
			return g.measure == it.measure && normUnit(g.unit) == normUnit(it.unit)
		})
		if idx < 0 {
			groups = append(groups, &poolGroup{measure: it.measure, unit: it.unit})
			idx = len(groups) - 1
		}

		groups[idx].items = append(groups[idx].items, *it)
	}

	best := groups[0]

	for _, g := range groups[1:] {
		if g.better(best) {
			best = g
		}
	}

	if len(groups) > 1 {
		p.log.Info("effects use several measures: pooling the largest group",
			"measure", best.measure, "studies", len(best.items), "groups", len(groups))
	}

	pe, err := pooledFrom(best)
	if err != nil {
		p.log.Warn("effects not pooled", "measure", best.measure, "err", err)
		return nil
	}

	return pe
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
