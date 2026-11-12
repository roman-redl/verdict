package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const regInconsistency = "NCT09999999"

func TestComparableStudies(t *testing.T) {
	// the second trial's means come with an SE of the arm mean instead of an SD
	seOnly := &model.Effect{
		Unit:         unitMmHg,
		Intervention: &model.Arm{Mean: new(10.0), SE: new(0.5), N: new(25.0)},
		Control:      &model.Arm{Mean: new(8.0), SE: new(0.5), N: new(25.0)},
	}

	tests := []struct {
		name    string
		studies []poolStudy
		want    int
	}{
		{
			name:    "no study",
			studies: nil,
			want:    0,
		},
		{
			name:    "one study with numbers",
			studies: []poolStudy{{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)}},
			want:    1,
		},
		{
			name: "one study with numbers, the other trial has none",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialB, design: model.DesignRCT},
			},
			want: 1,
		},
		{
			name: "two reports of one trial count once",
			studies: []poolStudy{
				{
					doi: trialA, year: 2008, ids: []string{regInconsistency},
					design: model.DesignRCT, effect: counts(10, 100, 20, 100),
				},
				{
					doi: trialB, year: 2013, ids: []string{regInconsistency},
					design: model.DesignRCT, effect: counts(9, 90, 19, 95),
				},
			},
			want: 1,
		},
		{
			name: "a second trial with numbers on another outcome does not count",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{
					doi: trialB, design: model.DesignRCT,
					effect: matched(counts(11, 100, 21, 100), model.OutcomeDifferent, model.TimepointYes, 0),
				},
				{
					doi: trialC, design: model.DesignRCT,
					effect: matched(counts(12, 100, 22, 100), model.OutcomeRelated, model.TimepointYes, 0),
				},
			},
			want: 1,
		},
		{
			name: "a second trial on the same outcome, kept out of the pool by its measure",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialB, design: model.DesignRCT, effect: reported("HR", 0.8, 0.6, 1.1)},
			},
			want: 2,
		},
		{
			name: "a second study kept out of the pool as non-randomized",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: cohortA, design: model.DesignCohort, effect: counts(11, 100, 25, 100)},
			},
			want: 2,
		},
		{
			name: "another time point is still the same outcome",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{
					doi: trialB, design: model.DesignRCT,
					effect: matched(counts(11, 100, 21, 100), model.OutcomeSame, model.TimepointNo, 0),
				},
			},
			want: 2,
		},
		{
			name: "a review is not a study",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: "10.1/review", design: model.DesignMetaAnalysis, effect: counts(30, 300, 60, 300)},
			},
			want: 1,
		},
		{
			name: "a paper that is not a study report does not count",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: "10.1/erratum", design: model.DesignRCT, effect: counts(11, 100, 21, 100), pubTypes: []string{pubErratum}},
			},
			want: 1,
		},
		{
			name: "a retracted study does not count",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialB, design: model.DesignRCT, effect: counts(11, 100, 21, 100), retracted: true},
			},
			want: 1,
		},
		{
			name: "a study that is not direct does not count",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialB, design: model.DesignRCT, effect: counts(11, 100, 21, 100), relevance: "indirect"},
			},
			want: 1,
		},
		{
			name: "numbers the code cannot use do not count",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialB, design: model.DesignRCT, effect: counts(0, 0, 0, 0)},
				{doi: trialC, design: model.DesignRCT, effect: &model.Effect{Outcome: outcomeName}},
			},
			want: 1,
		},
		{
			name: "an arm SD restored from the SE counts",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialB, design: model.DesignRCT, effect: seOnly},
			},
			want: 2,
		},
		{
			name: "a missing outcome match reads as the same outcome (an old critique)",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialB, design: model.DesignRCT, effect: counts(11, 100, 21, 100)},
				{doi: trialC, design: model.DesignRCT, effect: reported("OR", 0.7, 0.5, 0.98)},
			},
			want: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, comparableStudies(poolCorpus(tc.studies...)))
		})
	}
}

// The count does not depend on the pool: a pool that ended with one study
// beside a second one kept out still counts both.
func TestComparableStudiesIsNotThePoolsK(t *testing.T) {
	p, _ := poolPipeline(t)

	st := poolCorpus(
		poolStudy{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
		poolStudy{doi: trialB, design: model.DesignRCT, effect: reported("HR", 0.8, 0.6, 1.1)},
	)

	pe := p.poolEffects(st)

	require.NotNil(t, pe)
	assert.Equal(t, 1, pe.K)
	assert.Equal(t, 2, comparableStudies(st))
}

func TestSynthesisInputCarriesTheInconsistencyBasis(t *testing.T) {
	one, err := synthesisUserPrompt(poolCorpus(
		poolStudy{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
	))
	require.NoError(t, err)
	assert.Contains(t, one, `"inconsistency_basis":{"comparable_studies":1}`)

	none, err := synthesisUserPrompt(&State{Question: "q?"})
	require.NoError(t, err)
	assert.Contains(t, none, `"inconsistency_basis":{"comparable_studies":0}`, "present even with no study")
}

func TestSynthesisPromptHasTheInconsistencyRule(t *testing.T) {
	assert.Contains(t, synthesisSystemPrompt, "inconsistency_basis.comparable_studies")
	assert.Contains(t, synthesisSystemPrompt, "With fewer than 2 comparable studies, do not rate down for inconsistency")
	assert.Contains(t, synthesisSystemPrompt, "a lack of numbers in the other studies is a gap")
}

func TestReportRendersTheComparableStudies(t *testing.T) {
	two := 2

	got := RenderMarkdown(&State{Report: &model.Report{Verdict: "moderate", ComparableStudies: &two}})
	assert.Contains(t, got, "**Studies with numbers on the outcome:** 2\n\n")

	old := RenderMarkdown(&State{Report: &model.Report{Verdict: "moderate"}})
	assert.NotContains(t, old, "Studies with numbers on the outcome", "a report written before the field has no line")
}
