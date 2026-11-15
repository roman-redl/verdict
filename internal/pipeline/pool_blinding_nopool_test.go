package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// subjectiveOutcome is an effect with the labels only: a subjective outcome, no
// numbers (so nothing is pooled).
func subjectiveOutcome() *model.Effect { return withObjective(&model.Effect{}, false) }

// selfReportedOutcome is an effect with the labels only: an outcome the
// participants report themselves, objectivity not stated.
func selfReportedOutcome() *model.Effect { return &model.Effect{OutcomeSelfReported: new(true)} }

// trialOf is a randomized trial with the critic's blinding, effect and recorded
// number of participants (zero: not stated).
func trialOf(doi string, b model.Blinding, ef *model.Effect, size int) poolStudy {
	return poolStudy{doi: doi, design: model.DesignRCT, blinding: b, effect: ef, sizeReported: size}
}

// openTrial is an open-label trial of a subjective outcome.
func openTrial(doi string, size int) poolStudy {
	return trialOf(doi, model.BlindingOpen, subjectiveOutcome(), size)
}

// maskedTrial is a double-blind trial of a subjective outcome.
func maskedTrial(doi string, size int) poolStudy {
	return trialOf(doi, model.BlindingDouble, subjectiveOutcome(), size)
}

// designed is the study with another design.
func designed(s poolStudy, d model.StudyDesign) poolStudy {
	s.design = d
	return s
}

// registered is the study with a trial registration number.
func registered(s poolStudy, id string) poolStudy {
	s.ids = []string{id}
	return s
}

func TestBlindingBiasWithoutPool(t *testing.T) {
	open, masked := openTrial, maskedTrial

	retracted := masked("10.1/retracted", 5000)
	retracted.retracted = true

	indirect := masked("10.1/indirect-trial", 5000)
	indirect.relevance = "indirect"

	for _, tc := range []struct {
		name    string
		studies []poolStudy
		want    string // the share in the reason; empty: the rule does not fire
	}{
		{
			name:    "open subjective trials carry most of the participants",
			studies: []poolStudy{open(doiW1, 300), masked(doiW2, 100)},
			want:    "75%",
		},
		{
			name:    "open subjective trials carry less than half",
			studies: []poolStudy{open(doiW1, 100), masked(doiW2, 300)},
		},
		{
			name:    "exactly half does not count",
			studies: []poolStudy{open(doiW1, 100), masked(doiW2, 100)},
		},
		{
			name: "the labels of an effect on another outcome do not count",
			studies: []poolStudy{trialOf(doiW1, model.BlindingOpen,
				&model.Effect{OutcomeMatch: model.OutcomeDifferent, OutcomeObjective: new(false)}, 300)},
		},
		{
			name: "the labels of an effect on a related outcome do not count",
			studies: []poolStudy{trialOf(doiW1, model.BlindingOpen,
				&model.Effect{OutcomeMatch: model.OutcomeRelated, OutcomeObjective: new(false)}, 300)},
		},
		{
			name:    "an open trial of an objective outcome does not count",
			studies: []poolStudy{trialOf(doiW1, model.BlindingOpen, withObjective(&model.Effect{}, true), 300)},
		},
		{
			name: "a cluster and a crossover trial are randomized",
			studies: []poolStudy{
				designed(open(doiW1, 200), model.DesignClusterRCT),
				designed(open(doiW2, 200), model.DesignCrossoverRCT),
				masked(doiW3, 100),
			},
			want: "80%",
		},
		{
			name: "non-randomized studies are not in the denominator",
			studies: []poolStudy{
				open(doiW1, 100),
				designed(masked(cohortA, 10000), model.DesignCohort),
				designed(masked("10.1/nrt", 10000), model.DesignNonrandomizedTrial),
				designed(masked("10.1/unclear", 10000), model.DesignUnclear),
			},
			want: "100%",
		},
		{
			name: "reviews, retracted and indirect studies are not in the denominator",
			studies: []poolStudy{
				open(doiW1, 100),
				designed(masked("10.1/sr", 5000), model.DesignSystematicReview),
				retracted,
				indirect,
			},
			want: "100%",
		},
		{
			name:    "an unknown size weighs as the mean of the known sizes",
			studies: []poolStudy{open(doiW1, 0), masked(doiW2, 1000), open(doiW3, 900)},
			want:    "65%", // (950 + 900) / (950 + 1000 + 900); weighing it as 1 participant would give 47%
		},
		{
			name:    "no size known: the studies weigh the same, two of three",
			studies: []poolStudy{open(doiW1, 0), open(doiW2, 0), masked(doiW3, 0)},
			want:    "67%",
		},
		{
			name:    "no size known: one of two is half",
			studies: []poolStudy{open(doiW1, 0), masked(doiW2, 0)},
		},
		{
			name: "assessor-blinded trial of a self-reported outcome",
			studies: []poolStudy{
				trialOf(doiW1, model.BlindingAssessorOnly, selfReportedOutcome(), 300),
				masked(doiW2, 100),
			},
			want: "75%",
		},
		{
			name: "assessor-blinded trial of an outcome rated by someone else",
			studies: []poolStudy{
				trialOf(doiW1, model.BlindingAssessorOnly, subjectiveOutcome(), 300),
				masked(doiW2, 100),
			},
		},
		{
			name: "a trial without an effect has unknown labels and does not count",
			studies: []poolStudy{
				trialOf(doiW1, model.BlindingOpen, nil, 300),
				masked(doiW2, 100),
			},
		},
		{
			// counted twice it would be 200 / 350 = 57%
			name: "reports of one trial count once",
			studies: []poolStudy{
				registered(open(doiW1, 100), "NCT01"),
				registered(open(doiW2, 100), "NCT01"),
				masked(doiW3, 150),
			},
		},
		{name: "no studies"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, studies := evidenceStudies(poolCorpus(tc.studies...), nil)
			bb := blindingBiasWithoutPool(studies)

			if tc.want == "" {
				assert.Nil(t, bb)
				return
			}

			require.NotNil(t, bb)
			assert.Equal(t, blindingLevels, bb.Levels)
			assert.Contains(t, bb.Reason, "carry "+tc.want+" of the participants")
			assert.Contains(t, bb.Reason, "weighted by participants: there is no pooled estimate")
		})
	}
}

// The labels of a trial are read from its reports: the kept report's effect, and
// for a label it does not state the next report's.
func TestBlindingBiasWithoutPoolFoldsTheReportsOfATrial(t *testing.T) {
	st := poolCorpus(
		registered(trialOf(doiW1, model.BlindingOpen, nil, 100), "NCT02"),
		registered(trialOf(doiW2, "", subjectiveOutcome(), 0), "NCT02"),
		maskedTrial(doiW3, 100),
	)

	_, studies := evidenceStudies(st, nil)
	require.Len(t, studies, 2)

	assert.Equal(t, model.BlindingOpen, studies[0].blinding)
	require.NotNil(t, studies[0].objective)
	assert.False(t, *studies[0].objective)

	// 100 participants of the open subjective trial against 100: half
	assert.Nil(t, blindingBiasWithoutPool(studies))
}

// With a pooled estimate the rating is the pool's, by the pool weights; without
// one, the participants'. The same corpus gives each answer for its own weights.
func TestBlindingRatingFollowsThePool(t *testing.T) {
	p, _ := poolPipeline(t)

	// open: few events (small pool weight) among many participants; masked: many
	// events among few participants
	openMinority := poolCorpus(
		trialOf(doiW1, model.BlindingOpen, withObjective(counts(1, 1000, 2, 1000), false), 2000),
		trialOf(doiW2, model.BlindingDouble, withObjective(counts(50, 100, 100, 100), false), 200),
	)

	res := p.pool(openMinority)
	require.NotNil(t, res.est)
	assert.Nil(t, res.blinding, "the pool weights decide: the open trial is a small part of the pool")
	assert.Nil(t, blindingRating(openMinority, res), "by participants it would fire, but there is a pool")

	// the opposite: the open trial dominates the pool, a few participants
	openMajority := poolCorpus(
		trialOf(doiW1, model.BlindingOpen, withObjective(counts(100, 1000, 200, 1000), false), 20),
		trialOf(doiW2, model.BlindingDouble, withObjective(counts(5, 50, 9, 50), false), 2000),
	)

	res = p.pool(openMajority)
	require.NotNil(t, res.est)

	bb := blindingRating(openMajority, res)
	require.NotNil(t, bb)
	assert.Equal(t, res.blinding, bb)
	assert.Contains(t, bb.Reason, "of the pool weight")
	assert.NotContains(t, bb.Reason, "no pooled estimate")

	// no pool: the participants
	noPool := poolCorpus(openTrial(doiW1, 300), maskedTrial(doiW2, 100))

	res = p.pool(noPool)
	require.Nil(t, res.est)
	assert.Nil(t, res.blinding)

	bb = blindingRating(noPool, res)
	require.NotNil(t, bb)
	assert.Contains(t, bb.Reason, "no pooled estimate")
}

// The rating lowers the certainty by one level: the same evidence is one level
// lower with it than without it.
func TestBlindingWithoutPoolLowersTheGrade(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fired bool
		level string
		bias  int
	}{
		{name: "rated", fired: true, level: verdictModerate, bias: 1},
		{name: "not rated", level: verdictStrong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := computeGrade(certaintyInput{
				Randomized: true, BlindingFired: tc.fired, Imprecision: &model.Imprecision{},
				Checklist: noFlags(), Studies: oneStudy(),
			})

			assert.Equal(t, tc.level, got.Level)
			assert.Equal(t, tc.bias, got.RiskOfBias.Levels)
		})
	}
}

// openUnpooledBatch is two randomized trials of a subjective outcome that give
// no numbers, so nothing is pooled: a is open-label with 300 participants, b is
// double-blind with 100.
const openUnpooledBatch = `{"critiques":[` +
	`{"doi":"10.1000/a","design_quality":"high","relevance":"direct","result_direction":"null",` +
	`"study_design":"rct","blinding":"open","sample_size_reported":300,` +
	`"effect":{"outcome":"pain","outcome_objective":false}},` +
	`{"doi":"10.1000/b","design_quality":"high","relevance":"direct","result_direction":"null",` +
	`"study_design":"rct","blinding":"double_blind","sample_size_reported":100,` +
	`"effect":{"outcome":"pain","outcome_objective":false}}]}`

// The whole run without a pooled estimate: the rating reaches the state, the
// synthesis input and the computed certainty (one level lower).
func TestRunRatesBlindingBiasWithoutPool(t *testing.T) {
	p, llm, _ := loggedRun(t, openUnpooledBatch, synthesisReplyWith(t, "moderate", withImprecision(0)))

	st, _, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err)

	require.Nil(t, st.Pooled)
	require.NotNil(t, st.BlindingBias)
	assert.Equal(t, 1, st.BlindingBias.Levels)
	assert.Contains(t, st.BlindingBias.Reason, "carry 75% of the participants")
	assert.Contains(t, llm.prompts[1], `"blinding_bias":{"levels":1,`)

	require.NotNil(t, st.Report)
	assert.Equal(t, st.BlindingBias, st.Report.BlindingBias)

	g := st.Report.Grade
	require.NotNil(t, g)
	assert.Equal(t, "strong", g.Start)
	assert.Equal(t, []string{model.CauseBlinding}, g.RiskOfBias.Causes)
	assert.Equal(t, "moderate", g.Level)
}
