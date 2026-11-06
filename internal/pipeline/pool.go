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

	// Raw continuous data waits until the pool knows whether every such study
	// shares one unit (then MD) or not (then SMD).
	continuous bool
	md, smd    meta.Study
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
		if a, b, ok := countArms(ef.Intervention, ef.Control); ok {
			s, err := meta.LogRR(a[0], a[1], b[0], b[1])
			if err != nil {
				return pooledStudy{}, fmt.Errorf("counts: %w", err)
			}

			return pooledStudy{measure: model.MeasureRR, study: s, n: int(a[1] + b[1])}, nil
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

// countArms returns the events and totals of both arms when all four are given.
func countArms(i, c *model.Arm) (a, b [2]float64, ok bool) {
	if i.Events == nil || i.Total == nil || c.Events == nil || c.Total == nil {
		return a, b, false
	}

	return [2]float64{*i.Events, *i.Total}, [2]float64{*c.Events, *c.Total}, true
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

	return pooledStudy{
		continuous: true, unit: tidyUnit(unit), md: md, smd: smd, n: int(a.N + b.N),
	}, nil
}

// deriveReported handles a study that reports only an estimate and its 95% CI.
func deriveReported(ef *model.Effect) (pooledStudy, error) {
	m, err := model.ParseMeasure(ef.Measure)
	if err != nil {
		return pooledStudy{}, fmt.Errorf("estimate: %w", err)
	}

	if ef.Value == nil || ef.CILow == nil || ef.CIHigh == nil {
		return pooledStudy{}, fmt.Errorf("estimate %s: %w: the value or its interval is missing", m, errNoNumbers)
	}

	ps := pooledStudy{measure: m}

	if m.IsRatio() {
		ps.study, err = meta.RatioCI(*ef.Value, *ef.CILow, *ef.CIHigh)
	} else {
		ps.study, err = meta.DifferenceCI(*ef.Value, *ef.CILow, *ef.CIHigh)
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
		K: res.K, Studies: dois,
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

	return pe, nil
}
