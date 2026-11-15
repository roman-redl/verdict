package pipeline

// The pool of studies of the question's own outcome and of a related one: the
// studies of the question's own outcome drive every choice (the unit of a mean
// difference, the measure, the unified scale, the report of a trial); a related
// study joins what they build and builds a pool alone only without them.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	trialD  = "10.1/trial-d"
	unitPct = "%"
)

// mdIn is a continuous outcome in the unit: 10 against 8, SD 2, n per arm (a
// different n makes a different study, not a duplicate).
func mdIn(unit string, n float64) *model.Effect {
	return &model.Effect{
		Unit:         unit,
		Intervention: &model.Arm{Mean: new(10.0), SD: new(2.0), N: new(n)},
		Control:      &model.Arm{Mean: new(8.0), SD: new(2.0), N: new(n)},
	}
}

// ownOutcome and relatedOutcome label an effect as measuring the question's
// outcome or a related one, at the question's time point.
func ownOutcome(ef *model.Effect) *model.Effect {
	return matched(ef, model.OutcomeSame, model.TimepointYes, 0)
}

func relatedOutcome(ef *model.Effect) *model.Effect {
	return matched(ef, model.OutcomeRelated, model.TimepointYes, 0)
}

// trial is a randomized trial with the effect.
func trial(doi string, ef *model.Effect) poolStudy {
	return poolStudy{doi: doi, design: model.DesignRCT, effect: ef}
}

// The mean difference or the standardized one is decided by the studies of the
// question's own outcome: a surrogate in another unit does not turn the pool of
// the trials in mmHg into an SMD one. A related study follows the decision, and
// decides only when there is no study of the question's own outcome.
func TestSettleContinuousFollowsTheQuestionsOutcome(t *testing.T) {
	for _, tc := range []struct {
		name        string
		studies     []poolStudy
		wantMeasure model.Measure
		wantUnit    string
		wantStudies []string
		wantLeft    []string
	}{
		{
			name: "blood pressure in mmHg and a surrogate in percent: the mmHg trials, a mean difference",
			studies: []poolStudy{
				trial(trialA, ownOutcome(mdIn(unitMmHg, 100))), trial(trialB, ownOutcome(mdIn(unitMmHg, 150))),
				trial(trialC, relatedOutcome(mdIn(unitPct, 500))),
			},
			wantMeasure: model.MeasureMD, wantUnit: unitMmHg, wantStudies: []string{trialA, trialB},
			wantLeft: []string{trialC + " (MD)"},
		},
		{
			name: "a related study without a unit does not fit a mean difference either",
			studies: []poolStudy{
				trial(trialA, ownOutcome(mdIn(unitMmHg, 100))), trial(trialB, ownOutcome(mdIn(unitMmHg, 150))),
				trial(trialC, relatedOutcome(mdIn("", 500))),
			},
			wantMeasure: model.MeasureMD, wantUnit: unitMmHg, wantStudies: []string{trialA, trialB},
			wantLeft: []string{trialC + " (MD)"},
		},
		{
			name: "a related study in the same unit joins the mean difference",
			studies: []poolStudy{
				trial(trialA, ownOutcome(mdIn(unitMmHg, 100))), trial(trialB, ownOutcome(mdIn(unitMmHg, 150))),
				trial(trialC, relatedOutcome(mdIn(unitMmHg, 500))),
			},
			wantMeasure: model.MeasureMD, wantUnit: unitMmHg, wantStudies: []string{trialA, trialB, trialC},
		},
		{
			name: "the units of the question's own outcome differ: standardized, the related study joins",
			studies: []poolStudy{
				trial(trialA, ownOutcome(mdIn(unitMmHg, 100))), trial(trialB, ownOutcome(mdIn(unitPoints, 150))),
				trial(trialC, relatedOutcome(mdIn(unitPct, 500))),
			},
			wantMeasure: model.MeasureSMD, wantStudies: []string{trialA, trialB, trialC},
		},
		{
			name: "no study of the question's own outcome: the related ones decide, one unit",
			studies: []poolStudy{
				trial(trialA, relatedOutcome(mdIn(unitMmHg, 100))), trial(trialB, relatedOutcome(mdIn(unitMmHg, 150))),
			},
			wantMeasure: model.MeasureMD, wantUnit: unitMmHg, wantStudies: []string{trialA, trialB},
		},
		{
			name: "no study of the question's own outcome: the related ones decide, units differ",
			studies: []poolStudy{
				trial(trialA, relatedOutcome(mdIn(unitMmHg, 100))), trial(trialB, relatedOutcome(mdIn(unitPoints, 150))),
			},
			wantMeasure: model.MeasureSMD, wantStudies: []string{trialA, trialB},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			pe := p.poolEffects(poolCorpus(tc.studies...))

			require.NotNil(t, pe)
			assert.Equal(t, tc.wantMeasure, pe.Measure)
			assert.Equal(t, tc.wantUnit, pe.Unit)
			assert.Equal(t, tc.wantStudies, pe.Studies)
			assert.Equal(t, tc.wantLeft, pe.LeftOut)
		})
	}
}

// The pool is chosen by the studies of the question's own outcome: a candidate
// with none of them never beats one that has one, and the participants of
// related studies do not make a candidate win. Related studies alone build the
// pool only when no study of the question's own outcome has usable numbers.
func TestPoolChoiceRanksTheQuestionsOutcomeFirst(t *testing.T) {
	for _, tc := range []struct {
		name        string
		studies     []poolStudy
		wantMeasure model.Measure
		wantStudies []string
		wantLeft    []string
	}{
		{
			name: "cardiovascular death (RR, 800) against a surrogate, LDL (MD, 2000)",
			studies: []poolStudy{
				trial(trialA, ownOutcome(counts(40, 400, 60, 400))), trial(trialB, relatedOutcome(mdIn("mg/dL", 1000))),
			},
			wantMeasure: model.MeasureRR, wantStudies: []string{trialA}, wantLeft: []string{trialB + " (MD)"},
		},
		{
			name: "related participants do not make a group win: 800 of the outcome against 40 and 2000 related",
			studies: []poolStudy{
				trial(trialA, ownOutcome(counts(40, 400, 60, 400))), trial(trialB, ownOutcome(mdIn(unitMmHg, 20))),
				trial(trialC, relatedOutcome(mdIn(unitMmHg, 1000))),
			},
			wantMeasure: model.MeasureRR, wantStudies: []string{trialA},
			wantLeft: []string{trialB + " (MD)", trialC + " (MD)"},
		},
		{
			name: "a study of the outcome of unknown size still beats a related one",
			studies: []poolStudy{
				trial(trialA, ownOutcome(reported("HR", 0.7, 0.5, 0.98))),
				trial(trialB, relatedOutcome(counts(30, 1000, 60, 1000))),
			},
			wantMeasure: model.MeasureHR, wantStudies: []string{trialA}, wantLeft: []string{trialB + " (RR)"},
		},
		{
			name: "the number of studies of the outcome decides before a related study's participants",
			studies: []poolStudy{
				trial(trialA, ownOutcome(reported("HR", 0.7, 0.5, 0.98))),
				trial(trialB, ownOutcome(reported("HR", 0.8, 0.6, 1.07))),
				trial(trialC, ownOutcome(reported("RR", 0.9, 0.7, 1.16))),
				trial(trialD, relatedOutcome(counts(100, 1000, 200, 1000))),
			},
			wantMeasure: model.MeasureHR, wantStudies: []string{trialA, trialB},
			wantLeft: []string{trialC + " (RR)", trialD + " (RR)"},
		},
		{
			name: "a related study in one of two equal groups does not break the tie: the measure order does",
			studies: []poolStudy{
				trial(trialA, ownOutcome(counts(40, 400, 60, 400))),
				trial(trialB, relatedOutcome(reported("OR", 0.5, 0.3, 0.83))),
			},
			wantMeasure: model.MeasureRR, wantStudies: []string{trialA}, wantLeft: []string{trialB + " (OR)"},
		},
		{
			name: "no study of the question's own outcome: the related ones pool, the larger group",
			studies: []poolStudy{
				trial(trialA, relatedOutcome(counts(10, 100, 20, 100))), trial(trialB, relatedOutcome(counts(20, 200, 40, 200))),
				trial(trialC, relatedOutcome(mdIn(unitMmHg, 20))),
			},
			wantMeasure: model.MeasureRR, wantStudies: []string{trialA, trialB}, wantLeft: []string{trialC + " (MD)"},
		},
		{
			name: "no study of the question's own outcome, one related study: it is the pool",
			studies: []poolStudy{
				trial(trialA, relatedOutcome(counts(10, 100, 20, 100))),
			},
			wantMeasure: model.MeasureRR, wantStudies: []string{trialA},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			pe := p.poolEffects(poolCorpus(tc.studies...))

			require.NotNil(t, pe)
			assert.Equal(t, tc.wantMeasure, pe.Measure)
			assert.Equal(t, tc.wantStudies, pe.Studies)
			assert.Equal(t, tc.wantLeft, pe.LeftOut)
		})
	}
}

// The unified standardized pool takes a related study only when every study of
// it is related; otherwise the related study keeps its measure and stays out of
// the pool, and does not tip the choice between the pools.
func TestUnifiedPoolKeepsRelatedStudiesOut(t *testing.T) {
	harm := func(ef *model.Effect) *model.Effect { return withEventHarm(ef, true) }
	better := func(ef *model.Effect) *model.Effect { return withHigherBetter(ef, true) }

	for _, tc := range []struct {
		name        string
		state       func() *State
		wantMeasure model.Measure
		wantStudies []string
		wantLeft    []string
	}{
		{
			name: "a related study does not join the unified pool of the question's own outcome",
			state: func() *State {
				// the native RR pool is 30 + 200 + 2000; the unified pool of the outcome itself is 240
				// against the 230 of the outcome in the RR pool
				st := poolState(
					ownOutcome(reported("RR", 0.7, 0.5, 0.98)),
					ownOutcome(harm(counts(10, 100, 20, 100))),
					ownOutcome(better(means(2, unitPoints))),
					relatedOutcome(harm(counts(30, 1000, 60, 1000))),
				)
				st.Enriched[0].SampleSize = 30 // the reported estimate has no arms to count

				return st
			},
			wantMeasure: model.MeasureSMD, wantStudies: []string{study2, study3},
			wantLeft: []string{study1 + " (RR)", "10.1/s4 (RR)"},
		},
		{
			name: "only related studies: they are put on one scale",
			state: func() *State {
				return poolState(
					relatedOutcome(harm(counts(10, 100, 20, 100))),
					relatedOutcome(better(means(2, unitPoints))),
				)
			},
			wantMeasure: model.MeasureSMD, wantStudies: []string{study1, study2},
		},
		{
			name: "a related study that would make the unified pool larger stays out, the native pool is chosen",
			state: func() *State {
				return poolState(
					ownOutcome(harm(counts(10, 100, 20, 100))),
					relatedOutcome(better(means(2, unitPoints))),
				)
			},
			wantMeasure: model.MeasureRR, wantStudies: []string{study1}, wantLeft: []string{study2 + " (MD)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			pe := p.poolEffects(tc.state())

			require.NotNil(t, pe)
			assert.Equal(t, tc.wantMeasure, pe.Measure)
			assert.Equal(t, tc.wantMeasure == model.MeasureSMD, pe.Converted)
			assert.Equal(t, tc.wantStudies, pe.Studies)
			assert.ElementsMatch(t, tc.wantLeft, pe.LeftOut)
		})
	}
}

// Three related trials beside one of the question's own outcome, all on one
// dichotomous measure: all four are pooled and the related share is computed
// from their weights.
func TestPoolRelatedTrialsBesideOneOfTheOutcome(t *testing.T) {
	p, _ := poolPipeline(t)

	st := poolCorpus(
		trial(trialA, ownOutcome(counts(10, 100, 20, 100))),
		trial(trialB, relatedOutcome(counts(20, 200, 40, 200))),
		trial(trialC, relatedOutcome(counts(15, 150, 30, 150))),
		trial(trialD, relatedOutcome(counts(12, 120, 24, 120))),
	)

	res := p.pool(st)

	require.NotNil(t, res.est)
	assert.Equal(t, 4, res.est.K)
	assert.Equal(t, []string{trialA, trialB, trialC, trialD}, res.est.Studies)
	assert.Empty(t, res.est.LeftOut)

	st.Pooled = res.est
	got := p.assessRelatedOutcomes(st)

	require.NotNil(t, got)
	assert.InDelta(t, 1-res.est.Weights[0], got.Share, 1e-9, "the related studies carry the rest of the weight")
	assert.Greater(t, got.Share, 0.5, "the three related trials outweigh the one")
	assert.True(t, got.RatedDown)
}

// A single related study that is the whole estimate counts: its share is 1 and it
// is marked in the evidence base and in the list, although it is not pooled with
// anything. A single study of the question's own outcome carries no mark.
func TestSingleRelatedStudyIsMarked(t *testing.T) {
	for _, tc := range []struct {
		name        string
		effect      *model.Effect
		wantShare   float64
		wantRelated bool
	}{
		{name: "a related study", effect: relatedOutcome(counts(10, 100, 20, 100)), wantShare: 1, wantRelated: true},
		{name: "a study of the outcome", effect: ownOutcome(counts(10, 100, 20, 100)), wantShare: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			st := poolCorpus(trial(trialA, tc.effect))
			st.Pooled = p.pool(st).est

			require.NotNil(t, st.Pooled)
			require.Equal(t, model.PoolSingleStudy, st.Pooled.Method)

			ro := p.assessRelatedOutcomes(st)
			require.NotNil(t, ro)
			assert.InDelta(t, tc.wantShare, ro.Share, 1e-9)
			assert.Equal(t, tc.wantRelated, ro.RatedDown)

			eb := buildEvidenceBase(st, st.Pooled)
			require.NotNil(t, eb)
			require.Len(t, eb.Studies, 1)

			assert.False(t, eb.Studies[0].Pooled, "one study is not pooled with anything")
			assert.Equal(t, tc.wantRelated, eb.Studies[0].OutcomeRelated, "the mark agrees with the share")
			assert.Equal(t, tc.wantRelated, strings.Contains(english().studyList(eb), "related outcome"))
		})
	}
}

// The mark is on the study whose pooled report has a related outcome, not on
// the report that ranks first: here the best-ranked report has no numbers, so
// the pool takes the other one.
func TestRelatedMarkFollowsThePooledReport(t *testing.T) {
	p, _ := poolPipeline(t)

	const reg = "NCT00000001"

	st := poolCorpus(
		poolStudy{
			doi: trialA, year: 2010, ids: []string{reg}, design: model.DesignRCT,
			effect: matched(&model.Effect{Outcome: "outcome"}, model.OutcomeSame, model.TimepointYes, 52),
		},
		poolStudy{
			doi: trialB, year: 2012, ids: []string{reg}, design: model.DesignRCT,
			effect: matched(counts(20, 200, 40, 200), model.OutcomeRelated, model.TimepointYes, 52),
		},
		trial(trialC, ownOutcome(counts(10, 100, 20, 100))),
	)
	st.Pooled = p.pool(st).est

	require.NotNil(t, st.Pooled)
	assert.Equal(t, []string{trialB, trialC}, st.Pooled.Studies)

	eb := buildEvidenceBase(st, st.Pooled)
	require.NotNil(t, eb)
	require.Len(t, eb.Studies, 2)

	assert.Equal(t, trialA, eb.Studies[0].DOI, "the trial is named by its best-ranked report")
	assert.True(t, eb.Studies[0].Pooled)
	assert.True(t, eb.Studies[0].OutcomeRelated, "the pooled report is the related one")
	assert.False(t, eb.Studies[1].OutcomeRelated)
}

// A report without an extracted effect is no candidate of the pool, so it does
// not take the trial away from a related report with numbers because it reads
// as the question's own outcome: the pool takes the report that has the numbers
// and the trial's related mark follows it (the trial is named by the report
// that ranks first, here the one that matches the time point).
func TestReportWithoutEffectDoesNotOutrankRelatedOne(t *testing.T) {
	p, _ := poolPipeline(t)

	const reg = "NCT00000002"

	st := poolCorpus(
		poolStudy{doi: trialA, year: 2010, ids: []string{reg}, design: model.DesignRCT},
		poolStudy{
			doi: trialB, year: 2012, ids: []string{reg}, design: model.DesignRCT,
			effect: relatedOutcome(counts(20, 200, 40, 200)),
		},
		trial(trialC, ownOutcome(counts(10, 100, 20, 100))),
	)
	st.Pooled = p.pool(st).est

	require.NotNil(t, st.Pooled)

	eb := buildEvidenceBase(st, st.Pooled)
	require.NotNil(t, eb)
	require.Len(t, eb.Studies, 2)

	assert.Equal(t, trialB, eb.Studies[0].DOI, "the report with the numbers names the trial")
	assert.True(t, eb.Studies[0].OutcomeRelated)
}

// The synthesis prompt leaves the related label of the pooled studies to the
// code and keeps the judge for what the code did not count.
func TestSynthesisPromptNarrowsTheOutcomeRule(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{"the code rates the related label", `the pipeline already rates the pooled studies the critics labelled "related"`},
		{"the share is the pool weight", "related_outcomes.share is their share of the pool weight"},
		{"the label is not counted again", "do not count those again"},
		{
			"two cases only, when related_outcomes is present",
			`In that case, answer "yes" for outcome in two cases only: a different time frame than the question's`,
		},
		{
			"a surrogate with no related label",
			"(a surrogate) while related_outcomes.share is 0",
		},
		{"left-out related studies do not count", "related studies left out of the pool do not count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Contains(t, synthesisSystemPrompt, tc.want)
		})
	}

	assert.NotContains(t, synthesisSystemPrompt, "direct evidence outside the pool")

	assert.NotContains(t, synthesisSystemPrompt, `answer "yes" for outcome only for a different time frame`)
}
