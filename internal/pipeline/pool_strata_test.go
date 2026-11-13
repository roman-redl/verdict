package pipeline

import (
	"cmp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// poolStudy is one study of the stratum, outcome and duplicate tests: its design
// as the critic reads it, its extracted effect and what the metadata say.
type poolStudy struct {
	doi       string
	year      int
	ids       []string
	design    model.StudyDesign
	effect    *model.Effect
	relevance string // direct when empty
	pubTypes  []string
	retracted bool
	size      int // the metadata sample size
	// sizeReported is the sample size the critic recorded; it wins over size
	sizeReported int
}

// poolCorpus is a corpus of such studies in the order given; no discovery tag,
// so the critic's design is the only design there is.
func poolCorpus(studies ...poolStudy) *State {
	st := &State{}

	for i := range studies {
		s := &studies[i]
		e := model.EnrichedPaper{}
		e.DOI, e.Title, e.Year, e.SampleSize = s.doi, s.doi, s.year, s.size
		e.TrialRegistrations, e.PublicationTypes = s.ids, s.pubTypes
		e.Flags.Retracted = s.retracted

		c := model.Critique{
			DOI: s.doi, Relevance: cmp.Or(s.relevance, relevanceDirect), ResultDirection: directionPositive,
			StudyDesign: s.design, Effect: s.effect, SampleSizeReported: s.sizeReported,
		}

		st.Enriched = append(st.Enriched, e)
		st.Critiques = append(st.Critiques, c)
	}

	return st
}

// matched labels an effect with how it matches the question: the outcome, the
// time point and the follow-up in weeks (0: not stated).
func matched(ef *model.Effect, o model.OutcomeMatch, tp model.TimepointMatch, weeks float64) *model.Effect {
	ef.OutcomeMatch, ef.TimepointMatch = o, tp

	if weeks > 0 {
		ef.TimepointWeeks = new(weeks)
	}

	return ef
}

// adjustedEffect marks the estimate as adjusted for confounders.
func adjustedEffect(ef *model.Effect) *model.Effect {
	ef.Adjusted = true
	return ef
}

const (
	cohortA = "10.1/cohort-a"
	cohortB = "10.1/cohort-b"
	cohortC = "10.1/cohort-c"
	trialA  = "10.1/trial-a"
	trialB  = "10.1/trial-b"
	trialC  = "10.1/trial-c"
)

// The cohorts the first stratum rule keeps out: a registry cohort of adjusted
// hazard ratios and four trials with no usable numbers (the q159 shape) leave no
// pooled estimate at all, because the trials come first.
func TestPoolStratumTrialsWithoutNumbersLeaveNoPoolAndCohortsOut(t *testing.T) {
	p, logs := poolPipeline(t)

	st := poolCorpus(
		poolStudy{doi: trialA, design: model.DesignRCT},
		poolStudy{doi: trialB, design: model.DesignRCT},
		poolStudy{doi: trialC, design: model.DesignClusterRCT},
		poolStudy{doi: "10.1/trial-d", design: model.DesignCrossoverRCT},
		poolStudy{doi: cohortA, design: model.DesignCohort, effect: adjustedEffect(reported("HR", 0.55, 0.40, 0.75))},
		poolStudy{doi: cohortB, design: model.DesignCohort, effect: adjustedEffect(reported("HR", 0.60, 0.45, 0.80))},
		poolStudy{doi: cohortC, design: model.DesignCohort, effect: adjustedEffect(reported("HR", 0.50, 0.30, 0.85))},
	)

	res := p.pool(st)

	assert.Nil(t, res.est, "the trials have no numbers: nothing is pooled, and not the cohorts instead")
	require.Len(t, res.leftOut, 3)

	for _, l := range res.leftOut {
		assert.Equal(t, leftNonRandomized, l.kind)
	}

	assert.Contains(t, logs.String(), `"effects not pooled" reason=non-randomized count=3`)
	assert.Equal(t, "no pooled estimate; studies with numbers that were not pooled: 3 non-randomized",
		noPoolWarning(res))
	assert.Nil(t, p.poolEffects(st))
}

// A trial and two cohorts (the q274 shape): the trial is the pool, the cohorts
// are named as non-randomized.
func TestPoolStratumTrialBeatsCohorts(t *testing.T) {
	p, _ := poolPipeline(t)

	pe := p.poolEffects(poolCorpus(
		poolStudy{
			doi: cohortA, design: model.DesignCohort, size: 3000,
			effect: adjustedEffect(reported("HR", 0.5, 0.3, 0.8)),
		},
		poolStudy{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
		poolStudy{
			doi: cohortB, design: model.DesignCohort, size: 5000,
			effect: adjustedEffect(reported("HR", 0.6, 0.4, 0.9)),
		},
	))

	require.NotNil(t, pe)
	assert.Equal(t, model.StratumRandomized, pe.Stratum)
	assert.Equal(t, []string{trialA}, pe.Studies)
	assert.Equal(t, model.MeasureRR, pe.Measure, "the cohorts' larger numbers do not pick the measure")
	assert.Equal(t, []string{cohortA + " (non-randomized)", cohortB + " (non-randomized)"}, pe.LeftOut)
}

// Parallel, cluster and crossover trials are one randomized stratum.
func TestPoolStratumRandomizedDesigns(t *testing.T) {
	p, _ := poolPipeline(t)

	cl := counts(11, 100, 21, 100)
	cl.Cluster = &model.Cluster{AdjustedForClustering: true}

	pe := p.poolEffects(poolCorpus(
		poolStudy{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
		poolStudy{doi: trialB, design: model.DesignClusterRCT, effect: cl},
		poolStudy{doi: trialC, design: model.DesignCrossoverRCT, effect: counts(12, 100, 22, 100)},
	))

	require.NotNil(t, pe)
	assert.Equal(t, model.StratumRandomized, pe.Stratum)
	assert.Equal(t, []string{trialA, trialB, trialC}, pe.Studies)
	assert.Empty(t, pe.LeftOut)
}

// Without a randomized study the non-randomized stratum is pooled, and only
// from adjusted estimates: crude counts do not control confounding.
func TestPoolStratumNonRandomizedOnlyAdjusted(t *testing.T) {
	p, logs := poolPipeline(t)

	pe := p.poolEffects(poolCorpus(
		poolStudy{doi: cohortA, design: model.DesignCohort, effect: adjustedEffect(reported("HR", 0.55, 0.40, 0.75))},
		poolStudy{doi: cohortB, design: model.DesignCaseControl, effect: counts(10, 100, 20, 100)},
		poolStudy{
			doi: cohortC, design: model.DesignNonrandomizedTrial,
			effect: adjustedEffect(reported("HR", 0.65, 0.5, 0.85)),
		},
	))

	require.NotNil(t, pe)
	assert.Equal(t, model.StratumNonRandomized, pe.Stratum)
	assert.Equal(t, model.MeasureHR, pe.Measure)
	assert.Equal(t, []string{cohortA, cohortC}, pe.Studies)
	assert.Equal(t, []string{cohortB + " (unadjusted non-randomized)"}, pe.LeftOut)
	assert.Contains(t, logs.String(), `reason="unadjusted non-randomized" count=1`)
}

// Only the studies that count as direct, retracted-free primary reports decide
// the stratum: an indirect or a retracted trial does not keep the cohorts out.
func TestPoolStratumIgnoresIndirectRetractedAndReviews(t *testing.T) {
	cohort := poolStudy{doi: cohortA, design: model.DesignCohort, effect: adjustedEffect(reported("HR", 0.55, 0.4, 0.75))}

	for name, trial := range map[string]poolStudy{
		"an indirect trial":   {doi: trialA, design: model.DesignRCT, relevance: relevanceIndirect},
		"a retracted trial":   {doi: trialA, design: model.DesignRCT, retracted: true},
		"a systematic review": {doi: trialA, design: model.DesignSystematicReview},
		"an erratum":          {doi: trialA, design: model.DesignRCT, pubTypes: []string{pubErratum}},
	} {
		t.Run(name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			pe := p.poolEffects(poolCorpus(trial, cohort))

			require.NotNil(t, pe)
			assert.Equal(t, model.StratumNonRandomized, pe.Stratum)
			assert.Equal(t, []string{cohortA}, pe.Studies)
		})
	}
}

// A study whose design is not known is not guessed into a stratum: beside a
// trial it is left out, and a corpus of such studies pools as it always did,
// with no stratum.
func TestPoolStratumUnknownDesign(t *testing.T) {
	p, _ := poolPipeline(t)

	pe := p.poolEffects(poolCorpus(
		poolStudy{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
		poolStudy{doi: trialB, design: model.DesignUnclear, effect: counts(11, 100, 21, 100)},
		poolStudy{doi: trialC, effect: counts(12, 100, 22, 100)},
	))

	require.NotNil(t, pe)
	assert.Equal(t, []string{trialA}, pe.Studies)
	assert.Equal(t, []string{trialB + " (design unknown)", trialC + " (design unknown)"}, pe.LeftOut)

	pe = p.poolEffects(poolCorpus(
		poolStudy{doi: trialA, effect: counts(10, 100, 20, 100)},
		poolStudy{doi: trialB, design: model.DesignUnclear, effect: counts(11, 100, 21, 100)},
	))

	require.NotNil(t, pe)
	assert.Empty(t, pe.Stratum, "nothing is known of the designs")
	assert.Equal(t, []string{trialA, trialB}, pe.Studies)
}

// A trial on another outcome still counts as a direct randomized study: the
// cohorts stay out, and there is no pool.
func TestPoolStratumRandomizedOnAnotherOutcomeStillKeepsCohortsOut(t *testing.T) {
	p, _ := poolPipeline(t)

	res := p.pool(poolCorpus(
		poolStudy{
			doi: trialA, design: model.DesignRCT,
			effect: matched(counts(10, 100, 20, 100), model.OutcomeDifferent, model.TimepointYes, 0),
		},
		poolStudy{doi: cohortA, design: model.DesignCohort, effect: adjustedEffect(reported("HR", 0.5, 0.3, 0.8))},
	))

	assert.Nil(t, res.est)
	assert.Equal(t, []string{trialA + " (other outcome)", cohortA + " (non-randomized)"}, leftOutTexts(res.leftOut))
}

// Only an effect for the question's outcome at the question's time point is
// pooled; a missing label is "unknown", which does not exclude.
func TestPoolOutcomeAndTimepointMatch(t *testing.T) {
	const (
		same, related, different = model.OutcomeSame, model.OutcomeRelated, model.OutcomeDifferent
		yes, no, unclear         = model.TimepointYes, model.TimepointNo, model.TimepointUnclear
	)

	for _, tc := range []struct {
		name    string
		outcome model.OutcomeMatch
		time    model.TimepointMatch
		want    string // the reason it is left out; empty when pooled
	}{
		{"the same outcome at the time point", same, yes, ""},
		{"both labels missing, as in an old artifact", "", "", ""},
		{"an unclear time point is kept", same, unclear, ""},
		{"a missing outcome label reads as the same", "", yes, ""},
		{"a related outcome", related, yes, leftOtherOutcome},
		{"a different outcome", different, yes, leftOtherOutcome},
		{"another time point", same, no, leftOtherTime},
		{"another outcome beats another time point as the reason", different, no, leftOtherOutcome},
		{"a missing outcome label and another time point", "", no, leftOtherTime},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, logs := poolPipeline(t)

			res := p.pool(poolCorpus(
				poolStudy{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				poolStudy{doi: trialB, design: model.DesignRCT, effect: matched(counts(11, 100, 21, 100), tc.outcome, tc.time, 0)},
			))

			require.NotNil(t, res.est)

			if tc.want == "" {
				assert.Equal(t, []string{trialA, trialB}, res.est.Studies)
				assert.Empty(t, res.est.LeftOut)

				return
			}

			assert.Equal(t, []string{trialA}, res.est.Studies)
			assert.Equal(t, []string{trialB + " (" + tc.want + ")"}, res.est.LeftOut)
			assert.Contains(t, logs.String(), `"effects not pooled"`)
		})
	}
}
