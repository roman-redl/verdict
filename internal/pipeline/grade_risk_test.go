package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// weighted is a study that is reported under the given DOIs and has the weight.
func weighted(weight float64, dois ...string) riskStudy { return riskStudy{dois: dois, weight: weight} }

func TestRiskSharesAreWeighted(t *testing.T) {
	studies := []riskStudy{weighted(0.6, "10.1/a"), weighted(0.3, "10.1/b"), weighted(0.1, "10.1/c")}

	shares := riskShares(studies, []model.RiskOfBiasStudy{
		studyAnswer("10.1/a", model.CauseAttrition, model.CauseSelectiveOutcome),
		studyAnswer("10.1/b", model.CauseAttrition),
		studyAnswer("10.1/c", model.CauseStoppedEarlyForBenefit),
	})

	assert.InDelta(t, 0.9, shares[model.CauseAttrition], 1e-12)
	assert.InDelta(t, 0.6, shares[model.CauseSelectiveOutcome], 1e-12)
	assert.InDelta(t, 0.1, shares[model.CauseStoppedEarlyForBenefit], 1e-12)
	assert.Zero(t, shares[model.CauseAllocationFlaw])
	assert.Len(t, shares, 4, "every item has a share")
}

// The share is of the weight of the studies, not of their number, and it is
// taken over all of them: the weights need not add up to one.
func TestRiskSharesNormalizeByTheTotalWeight(t *testing.T) {
	studies := []riskStudy{weighted(300, "10.1/a"), weighted(100, "10.1/b")}

	shares := riskShares(studies, []model.RiskOfBiasStudy{studyAnswer("10.1/a", model.CauseAttrition)})

	assert.InDelta(t, 0.75, shares[model.CauseAttrition], 1e-12)
}

func TestRiskSharesWithNothingToWeigh(t *testing.T) {
	for name, studies := range map[string][]riskStudy{
		"no direct primary study": nil, "all weights zero": {weighted(0, "10.1/a")},
	} {
		shares := riskShares(studies, []model.RiskOfBiasStudy{studyAnswer("10.1/a", model.CauseAttrition)})

		assert.Zero(t, shares[model.CauseAttrition], name)
	}
}

func TestRiskSharesMatchAnswersToStudies(t *testing.T) {
	studies := []riskStudy{weighted(0.5, "10.1000/a"), weighted(0.5, "10.1000/b1", "10.1000/b2")}

	t.Run("a study without an answer counts as no on every item", func(t *testing.T) {
		shares := riskShares(studies, []model.RiskOfBiasStudy{studyAnswer("10.1000/a", model.CauseAttrition)})

		assert.InDelta(t, 0.5, shares[model.CauseAttrition], 1e-12, "b is missing: no")
	})

	t.Run("an empty answer list is all no", func(t *testing.T) {
		shares := riskShares(studies, []model.RiskOfBiasStudy{})

		for cause, share := range shares {
			assert.Zero(t, share, cause)
		}
	})

	t.Run("a DOI that is not a direct primary study is ignored", func(t *testing.T) {
		shares := riskShares(studies, []model.RiskOfBiasStudy{
			studyAnswer("10.1000/a"),
			studyAnswer("10.1000/review", model.CauseAttrition),
			studyAnswer("", model.CauseAttrition),
		})

		assert.Zero(t, shares[model.CauseAttrition])
	})

	t.Run("a DOI is matched in its normalized form", func(t *testing.T) {
		shares := riskShares(studies, []model.RiskOfBiasStudy{
			studyAnswer("https://doi.org/10.1000/A", model.CauseAttrition),
		})

		assert.InDelta(t, 0.5, shares[model.CauseAttrition], 1e-12)
	})

	t.Run("a trial is answered under any of its reports, and one yes is a yes", func(t *testing.T) {
		shares := riskShares(studies, []model.RiskOfBiasStudy{
			studyAnswer("10.1000/b1"),
			studyAnswer("10.1000/b2", model.CauseSelectiveOutcome),
		})

		assert.InDelta(t, 0.5, shares[model.CauseSelectiveOutcome], 1e-12, "the study is counted once")
		assert.Zero(t, shares[model.CauseAttrition])
	})
}

func TestRiskOfBiasDomainByWeight(t *testing.T) {
	two := []riskStudy{weighted(0.5, "10.1/a"), weighted(0.5, "10.1/b")}
	uneven := []riskStudy{weighted(0.51, "10.1/a"), weighted(0.49, "10.1/b")}
	noisy := []riskStudy{
		weighted(0.1, "10.1/a"), weighted(0.2, "10.1/b"), weighted(0.2, "10.1/c"), weighted(0.5, "10.1/d"),
	}

	items := func(answers ...model.RiskOfBiasStudy) *model.RiskOfBiasItems {
		return &model.RiskOfBiasItems{PerStudy: append([]model.RiskOfBiasStudy{}, answers...)} // non-nil: answered
	}

	for _, tc := range []struct {
		name     string
		blinding bool
		studies  []riskStudy
		items    *model.RiskOfBiasItems
		levels   int
		causes   []string
	}{
		{
			name: "more than half of the weight counts", studies: uneven,
			items:  items(studyAnswer("10.1/a", model.CauseAttrition)),
			levels: 1, causes: []string{model.CauseAttrition},
		},
		{
			name: "exactly half of the weight does not count", studies: two,
			items: items(studyAnswer("10.1/a", model.CauseAttrition)),
		},
		{
			name: "half of the weight with rounding noise does not count", studies: noisy,
			items: items(
				studyAnswer("10.1/a", model.CauseAttrition), studyAnswer("10.1/b", model.CauseAttrition),
				studyAnswer("10.1/c", model.CauseAttrition),
			),
		},
		{
			name: "less than half does not count", studies: uneven,
			items: items(studyAnswer("10.1/b", model.CauseAttrition)),
		},
		{
			name: "two items are two levels", studies: two,
			items: items(
				studyAnswer("10.1/a", model.CauseAttrition, model.CauseAllocationFlaw),
				studyAnswer("10.1/b", model.CauseAttrition, model.CauseAllocationFlaw),
			),
			levels: 2, causes: []string{model.CauseAttrition, model.CauseAllocationFlaw},
		},
		{
			name: "a counting item and one that does not are one level", studies: uneven,
			items: items(
				studyAnswer("10.1/a", model.CauseAttrition),
				studyAnswer("10.1/b", model.CauseSelectiveOutcome),
			),
			levels: 1, causes: []string{model.CauseAttrition},
		},
		{
			name: "blinding alone is one level", blinding: true, studies: two, items: items(studyAnswer("10.1/a")),
			levels: 1, causes: []string{model.CauseBlinding},
		},
		{
			name: "blinding and one item are two levels", blinding: true, studies: uneven,
			items:  items(studyAnswer("10.1/a", model.CauseSelectiveOutcome)),
			levels: 2, causes: []string{model.CauseBlinding, model.CauseSelectiveOutcome},
		},
		{
			name: "risk of bias is capped at two levels", blinding: true, studies: two,
			items: items(
				studyAnswer("10.1/a", model.CauseAttrition, model.CauseSelectiveOutcome, model.CauseStoppedEarlyForBenefit),
				studyAnswer("10.1/b", model.CauseAttrition, model.CauseSelectiveOutcome, model.CauseStoppedEarlyForBenefit),
			),
			levels: 2, causes: []string{
				model.CauseBlinding, model.CauseAttrition, model.CauseSelectiveOutcome, model.CauseStoppedEarlyForBenefit,
			},
		},
		{
			name: "no studies and no answers is no lowering", items: items(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := riskOfBiasDomain(tc.blinding, tc.studies, tc.items)

			assert.Equal(t, tc.levels, got.Levels)
			assert.Equal(t, tc.causes, got.Causes)
			assert.Len(t, got.Shares, 4, "the share of every item is stored, counting or not")
		})
	}
}

// An older reply has one global answer per item and no per_study: it counts as
// before, with no shares.
func TestRiskOfBiasDomainGlobalAnswers(t *testing.T) {
	got := riskOfBiasDomain(true, oneStudy(), &model.RiskOfBiasItems{
		Attrition: model.Yes, AllocationFlaw: model.No, SelectiveOutcome: model.Yes, StoppedEarlyForBenefit: model.No,
	})

	assert.Equal(t, model.GradeDomain{
		Levels: 2, Causes: []string{model.CauseBlinding, model.CauseAttrition, model.CauseSelectiveOutcome},
	}, got)

	none := riskOfBiasDomain(false, nil, &model.RiskOfBiasItems{
		Attrition: model.No, AllocationFlaw: model.No, SelectiveOutcome: model.No, StoppedEarlyForBenefit: model.No,
	})

	assert.Zero(t, none.Levels)
	assert.Nil(t, none.Shares)
}

// The fallbacks of a reply that does not have the expected shape are logged.
func TestWarnOnFallbacks(t *testing.T) {
	cl := noFlags()
	p, _, logs := loggedRun(t)

	p.warnOnFallbacks(&State{Imprecision: &model.Imprecision{}}, &cl)
	assert.Empty(t, logs.String(), "a per-study reply with a pooled imprecision needs no fallback")

	cl.RiskOfBias = model.RiskOfBiasItems{Attrition: model.No}
	cl.ImprecisionWithoutPool = nil

	p.warnOnFallbacks(&State{}, &cl)
	assert.Contains(t, logs.String(), "risk of bias read from the global answers")
	assert.Contains(t, logs.String(), "imprecision taken as 0")

	// a per-study reply with repairs says how many
	logs.Reset()

	cl = noFlags()
	cl.RiskOfBias.Repairs = model.PerStudyRepairs{ItemsFilled: 3, EntriesDropped: 1}

	p.warnOnFallbacks(&State{Imprecision: &model.Imprecision{}}, &cl)
	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), "risk of bias per_study repaired")
	assert.Contains(t, logs.String(), "items_filled=3")
	assert.Contains(t, logs.String(), "entries_dropped=1")
}

func TestRiskStudiesWeightedByParticipants(t *testing.T) {
	sized := func(doi string, n int) evidenceStudy {
		return evidenceStudy{design: model.DesignRCT, size: n, dois: []string{doi}}
	}

	t.Run("the known participants are the weights", func(t *testing.T) {
		got, byPool := riskStudiesFor([]evidenceStudy{sized("a", 300), sized("b", 100)}, nil)

		assert.False(t, byPool)
		assert.Equal(t, []riskStudy{weighted(300, "a"), weighted(100, "b")}, got)
	})

	t.Run("an unknown size weighs as the mean of the known ones", func(t *testing.T) {
		got, _ := riskStudiesFor([]evidenceStudy{sized("a", 300), sized("b", 100), sized("c", 0)}, nil)

		assert.Equal(t, []riskStudy{weighted(300, "a"), weighted(100, "b"), weighted(200, "c")}, got)
	})

	t.Run("with no known size all weigh the same", func(t *testing.T) {
		got, _ := riskStudiesFor([]evidenceStudy{sized("a", 0), sized("b", 0)}, nil)

		assert.Equal(t, []riskStudy{weighted(1, "a"), weighted(1, "b")}, got)
	})

	t.Run("reviews are not direct primary studies", func(t *testing.T) {
		review := evidenceStudy{design: model.DesignMetaAnalysis, size: 9000, dois: []string{"r"}}

		got, _ := riskStudiesFor([]evidenceStudy{review, sized("a", 50)}, nil)

		assert.Equal(t, []riskStudy{weighted(50, "a")}, got)
	})

	t.Run("a pooled estimate without weights is not used", func(t *testing.T) {
		pe := &model.PooledEstimate{Method: model.PoolRandomEffects, Studies: []string{"a", "b"}}
		studies := []evidenceStudy{sized("a", 300), sized("b", 100)}
		studies[0].weight = 0.9

		got, byPool := riskStudiesFor(studies, pe)

		assert.False(t, byPool)
		assert.Equal(t, []riskStudy{weighted(300, "a"), weighted(100, "b")}, got)
	})
}

// The pool's weights, through the whole chain from the pooled estimate to the
// share: a trial reported twice has the weight of the report the pool kept, a
// direct study outside the pool has none.
func TestRiskWeightsUseThePoolWeights(t *testing.T) {
	const trial1a, trial1b, trial2 = "10.1/t1a", "10.1/t1b", "10.1/t2"

	st := evState(
		evPaper{doi: trial1a, design: model.DesignRCT, size: 100, regs: []string{regNCT}, year: 2019},
		evPaper{doi: trial1b, design: model.DesignRCT, size: 200, regs: []string{regNCT}, year: 2020},
		evPaper{doi: trial2, design: model.DesignRCT, size: 300},
		evPaper{doi: "10.1/left", design: model.DesignCohort, size: 5000},
		evPaper{doi: "10.1/risk-review", design: model.DesignMetaAnalysis, size: 9000},
	)
	st.Pooled = &model.PooledEstimate{
		Method: model.PoolRandomEffects, K: 2, Studies: []string{trial1b, trial2}, Weights: []float64{0.7, 0.3},
	}

	p, _, logs := loggedRun(t)
	got := p.riskWeights(st)

	require.Len(t, got, 3, "two trials and the cohort; the review is no primary study")
	assert.InDelta(t, 0.7, got[0].weight, 1e-12, "the trial takes the weight of its pooled report")
	assert.ElementsMatch(t, []string{trial1a, trial1b}, got[0].dois, "its answer may name either report")
	assert.InDelta(t, 0.3, got[1].weight, 1e-12)
	assert.Zero(t, got[2].weight, "a study outside the pool carries no weight")
	assert.Empty(t, logs.String())

	shares := riskShares(got, []model.RiskOfBiasStudy{
		studyAnswer(trial1a, model.CauseAttrition), studyAnswer("10.1/left", model.CauseAttrition),
	})

	assert.InDelta(t, 0.7, shares[model.CauseAttrition], 1e-12, "the answer for the other report counts for the trial")
}

// Without a pooled estimate the participants decide, trials counted once.
func TestRiskWeightsWithoutAPoolUseParticipants(t *testing.T) {
	st := evState(
		evPaper{doi: "10.1/u1a", design: model.DesignRCT, size: 100, regs: []string{regNCT}, year: 2019},
		evPaper{doi: "10.1/u1b", design: model.DesignRCT, size: 150, regs: []string{regNCT}, year: 2020},
		evPaper{doi: "10.1/u2", design: model.DesignRCT},
		evPaper{doi: "10.1/u3", design: model.DesignCohort, size: 50},
	)

	p, _, _ := loggedRun(t)
	got := p.riskWeights(st)

	require.Len(t, got, 3)
	assert.InDelta(t, 150, got[0].weight, 1e-12, "the trial counts once, with its size")
	assert.InDelta(t, 100, got[1].weight, 1e-12, "the unknown size weighs as the mean of the known ones (150 and 50)")
	assert.InDelta(t, 50, got[2].weight, 1e-12)
}

// A paper without a DOI is listed to the synthesis by its title alone, so its
// risk-of-bias answer is matched by the title (case and spaces aside); an
// answer with an empty doi matches nothing.
func TestRiskAnswersMatchAStudyWithoutADOIByTitle(t *testing.T) {
	const title = "A Trial Without a DOI"

	st := evState(
		evPaper{doi: "10.1/with", design: model.DesignRCT, size: 100},
		evPaper{title: title, design: model.DesignRCT, size: 300},
	)

	p, _, _ := loggedRun(t)
	studies := p.riskWeights(st)
	require.Len(t, studies, 2)

	for name, tc := range map[string]struct {
		doi  string
		want float64
	}{
		"the title as written":     {title, 0.75},
		"the title in other case":  {"  a trial WITHOUT a doi ", 0.75},
		"the DOI of the other one": {"https://doi.org/10.1/WITH", 0.25},
		"an empty doi":             {"", 0},
		"another title":            {"A trial of something else", 0},
	} {
		t.Run(name, func(t *testing.T) {
			shares := riskShares(studies, []model.RiskOfBiasStudy{studyAnswer(tc.doi, model.CauseAttrition)})

			assert.InDelta(t, tc.want, shares[model.CauseAttrition], 1e-12)
		})
	}
}

// Pool weights that match no direct primary study cannot come from the pool:
// the participants are used and the state is logged.
func TestRiskWeightsLogWeightsThatMatchNoStudy(t *testing.T) {
	st := evState(evPaper{doi: "10.1/t1", design: model.DesignRCT, size: 100})
	st.Pooled = &model.PooledEstimate{
		Method: model.PoolRandomEffects, Studies: []string{"10.1/elsewhere"}, Weights: []float64{1},
	}

	p, _, logs := loggedRun(t)
	got := p.riskWeights(st)

	require.Len(t, got, 1)
	assert.InDelta(t, 100, got[0].weight, 1e-12)
	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), "risk of bias weighted by participants")
}

// The pool keeps its weights next to its studies.
func TestPooledEstimateCarriesTheWeights(t *testing.T) {
	p, _ := poolPipeline(t)

	st := poolState(counts(10, 100, 20, 100), counts(11, 100, 21, 100))
	pe := p.poolEffects(st)

	require.NotNil(t, pe)
	require.Len(t, pe.Weights, len(pe.Studies))
	assert.InDelta(t, 1.0, pe.Weights[0]+pe.Weights[1], 1e-9)

	single := p.poolEffects(poolState(counts(10, 100, 20, 100)))

	require.NotNil(t, single)
	assert.Equal(t, []float64{1}, single.Weights)
}
