package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// gradeChecklistReply is the grade_checklist member of a canned synthesis
// reply (with its trailing comma): no flag is raised, the large effect is
// "yes", so that evidence of an unknown design (the weak start) reads moderate,
// and without a pooled estimate the imprecision is rated one level (a pooled
// estimate drops that answer).
const gradeChecklistReply = `"grade_checklist":{` +
	`"risk_of_bias":{"per_study":[{"doi":"10.1000/a","attrition":"no","allocation_flaw":"no",` +
	`"selective_outcome":"no","stopped_early_for_benefit":"no"}],"note":"n"},` +
	`"inconsistency":{"directions_conflict":"no","sizes_differ_beyond_chance":"no","note":"n"},` +
	`"indirectness":{"population":"no","intervention":"no","comparator":"no","outcome":"no","note":"n"},` +
	`"publication_bias":{"suspected":"no","note":"n"},` +
	`"large_effect":"yes","imprecision_without_pool":1},`

// studyAnswer is one per-study risk-of-bias answer with the named items yes.
func studyAnswer(doi string, yes ...string) model.RiskOfBiasStudy {
	s := model.RiskOfBiasStudy{
		DOI: doi, Attrition: model.No, AllocationFlaw: model.No, SelectiveOutcome: model.No,
		StoppedEarlyForBenefit: model.No,
	}

	for _, cause := range yes {
		switch cause {
		case model.CauseAttrition:
			s.Attrition = model.Yes
		case model.CauseAllocationFlaw:
			s.AllocationFlaw = model.Yes
		case model.CauseSelectiveOutcome:
			s.SelectiveOutcome = model.Yes
		case model.CauseStoppedEarlyForBenefit:
			s.StoppedEarlyForBenefit = model.Yes
		}
	}

	return s
}

// oneStudy is a single direct primary study that carries all the weight.
func oneStudy() []riskStudy { return []riskStudy{{dois: []string{listDOIA}, weight: 1}} }

// noFlags is a checklist that raises nothing: one per-study answer for the
// study of oneStudy, and the imprecision rated one level (read only without a
// pooled estimate).
func noFlags() model.GradeChecklist {
	return model.GradeChecklist{
		RiskOfBias:    model.RiskOfBiasItems{PerStudy: []model.RiskOfBiasStudy{studyAnswer(listDOIA)}},
		Inconsistency: model.InconsistencyItems{DirectionsConflict: model.No, SizesDifferBeyondChance: model.No},
		Indirectness: model.IndirectnessItems{
			Population: model.No, Intervention: model.No, Comparator: model.No, Outcome: model.No,
		},
		PublicationBias:        model.PublicationBiasItem{Suspected: model.No},
		LargeEffect:            model.No,
		ImprecisionWithoutPool: new(1),
	}
}

// withImprecision is noFlags with the model's imprecision rating set.
func withImprecision(levels int) model.GradeChecklist {
	c := noFlags()
	c.ImprecisionWithoutPool = &levels

	return c
}

// synthesisReplyWith is a synthesis answer with the given verdict and checklist.
func synthesisReplyWith(t *testing.T, verdict string, cl model.GradeChecklist) string {
	t.Helper()

	b, err := json.Marshal(cl)
	require.NoError(t, err)

	return `{"verdict":"` + verdict + `","verdict_rationale":"r","grade_checklist":` + string(b) +
		`,"consensus_status":"emerging","consensus":"leans positive"}`
}

func TestComputeGradeArithmetic(t *testing.T) {
	yes := func(mod func(c *model.GradeChecklist)) model.GradeChecklist {
		c := noFlags()
		mod(&c)

		return c
	}
	imprecise := func(levels int) *model.Imprecision { return &model.Imprecision{Levels: levels} }

	for _, tc := range []struct {
		name string
		in   certaintyInput
		want string
		// the domain levels: risk of bias, inconsistency, indirectness, imprecision, publication bias
		domains [5]int
		upgrade int
		// withoutPool leaves the input without a computed imprecision; the other
		// cases get a computed one that is not rated down
		withoutPool bool
	}{
		{
			name: "randomized with nothing raised stays strong",
			in:   certaintyInput{Randomized: true, Checklist: noFlags()}, want: verdictStrong,
		},
		{
			name: "non-randomized with nothing raised stays weak",
			in:   certaintyInput{Checklist: noFlags()}, want: verdictWeak,
		},
		{
			name: "attrition lowers risk of bias by one",
			in: certaintyInput{Randomized: true, Checklist: yes(func(c *model.GradeChecklist) {
				c.RiskOfBias.PerStudy[0].Attrition = model.Yes
			})},
			want: verdictModerate, domains: [5]int{1, 0, 0, 0, 0},
		},
		{
			name: "an allocation flaw lowers risk of bias by one",
			in: certaintyInput{Randomized: true, Checklist: yes(func(c *model.GradeChecklist) {
				c.RiskOfBias.PerStudy[0].AllocationFlaw = model.Yes
			})},
			want: verdictModerate, domains: [5]int{1, 0, 0, 0, 0},
		},
		{
			name: "a selective outcome lowers risk of bias by one",
			in: certaintyInput{Randomized: true, Checklist: yes(func(c *model.GradeChecklist) {
				c.RiskOfBias.PerStudy[0].SelectiveOutcome = model.Yes
			})},
			want: verdictModerate, domains: [5]int{1, 0, 0, 0, 0},
		},
		{
			name: "stopping early for benefit lowers risk of bias by one",
			in: certaintyInput{Randomized: true, Checklist: yes(func(c *model.GradeChecklist) {
				c.RiskOfBias.PerStudy[0].StoppedEarlyForBenefit = model.Yes
			})},
			want: verdictModerate, domains: [5]int{1, 0, 0, 0, 0},
		},
		{
			name: "the computed lack of blinding is one level",
			in:   certaintyInput{Randomized: true, BlindingFired: true, Checklist: noFlags()},
			want: verdictModerate, domains: [5]int{1, 0, 0, 0, 0},
		},
		{
			name: "blinding and attrition make risk of bias very serious",
			in: certaintyInput{Randomized: true, BlindingFired: true, Checklist: yes(func(c *model.GradeChecklist) {
				c.RiskOfBias.PerStudy[0].Attrition = model.Yes
			})},
			want: verdictWeak, domains: [5]int{2, 0, 0, 0, 0},
		},
		{
			name: "risk of bias is capped at two levels",
			in: certaintyInput{Randomized: true, BlindingFired: true, Checklist: yes(func(c *model.GradeChecklist) {
				c.RiskOfBias.PerStudy[0].Attrition = model.Yes
				c.RiskOfBias.PerStudy[0].AllocationFlaw = model.Yes
				c.RiskOfBias.PerStudy[0].SelectiveOutcome = model.Yes
				c.RiskOfBias.PerStudy[0].StoppedEarlyForBenefit = model.Yes
			})},
			want: verdictWeak, domains: [5]int{2, 0, 0, 0, 0},
		},
		{
			name: "inconsistency counts with two comparable studies",
			in: certaintyInput{Randomized: true, Comparable: 2, Checklist: yes(func(c *model.GradeChecklist) {
				c.Inconsistency.DirectionsConflict = model.Yes
			})},
			want: verdictModerate, domains: [5]int{0, 1, 0, 0, 0},
		},
		{
			name: "both inconsistency items are still one level",
			in: certaintyInput{Randomized: true, Comparable: 5, Checklist: yes(func(c *model.GradeChecklist) {
				c.Inconsistency.DirectionsConflict = model.Yes
				c.Inconsistency.SizesDifferBeyondChance = model.Yes
			})},
			want: verdictModerate, domains: [5]int{0, 1, 0, 0, 0},
		},
		{
			name: "inconsistency is ignored with one comparable study",
			in: certaintyInput{Randomized: true, Comparable: 1, Checklist: yes(func(c *model.GradeChecklist) {
				c.Inconsistency.SizesDifferBeyondChance = model.Yes
			})},
			want: verdictStrong,
		},
		{
			name: "inconsistency is ignored with no comparable study",
			in: certaintyInput{Randomized: true, Checklist: yes(func(c *model.GradeChecklist) {
				c.Inconsistency.DirectionsConflict = model.Yes
			})},
			want: verdictStrong,
		},
		{
			name: "indirectness is one level however many elements differ",
			in: certaintyInput{Randomized: true, Checklist: yes(func(c *model.GradeChecklist) {
				c.Indirectness.Population = model.Yes
				c.Indirectness.Outcome = model.Yes
				c.Indirectness.Comparator = model.Yes
			})},
			want: verdictModerate, domains: [5]int{0, 0, 1, 0, 0},
		},
		{
			name: "the computed imprecision is used as it is",
			in:   certaintyInput{Randomized: true, Imprecision: imprecise(2), Checklist: noFlags()},
			want: verdictWeak, domains: [5]int{0, 0, 0, 2, 0},
		},
		{
			name: "without a pooled estimate the model's imprecision_without_pool 2 is very serious",
			in:   certaintyInput{Randomized: true, Checklist: withImprecision(2)},
			want: verdictWeak, domains: [5]int{0, 0, 0, 2, 0}, withoutPool: true,
		},
		{
			name: "without a pooled estimate the model's imprecision_without_pool 1 is serious",
			in:   certaintyInput{Randomized: true, Checklist: withImprecision(1)},
			want: verdictModerate, domains: [5]int{0, 0, 0, 1, 0}, withoutPool: true,
		},
		{
			name: "without a pooled estimate the model's imprecision_without_pool 0 lowers nothing",
			in:   certaintyInput{Randomized: true, Checklist: withImprecision(0)},
			want: verdictStrong, withoutPool: true,
		},
		{
			name: "without a pooled estimate a rating above 2 is capped at two levels",
			in:   certaintyInput{Randomized: true, Checklist: withImprecision(7)},
			want: verdictWeak, domains: [5]int{0, 0, 0, 2, 0}, withoutPool: true,
		},
		{
			name: "without a pooled estimate a negative rating is no lowering",
			in:   certaintyInput{Randomized: true, Checklist: withImprecision(-3)},
			want: verdictStrong, withoutPool: true,
		},
		{
			name: "without a pooled estimate a missing rating is no lowering",
			in: certaintyInput{Randomized: true, Checklist: yes(func(c *model.GradeChecklist) {
				c.ImprecisionWithoutPool = nil
			})},
			want: verdictStrong, withoutPool: true,
		},
		{
			name: "the imprecision of the pool is not replaced by the model's rating",
			in:   certaintyInput{Randomized: true, Imprecision: imprecise(0), Checklist: withImprecision(2)},
			want: verdictStrong,
		},
		{
			name: "publication bias is one level",
			in: certaintyInput{Randomized: true, Checklist: yes(func(c *model.GradeChecklist) {
				c.PublicationBias.Suspected = model.Yes
			})},
			want: verdictModerate, domains: [5]int{0, 0, 0, 0, 1},
		},
		{
			name: "a large effect raises non-randomized evidence",
			in: certaintyInput{Checklist: yes(func(c *model.GradeChecklist) {
				c.LargeEffect = model.Yes
			})},
			want: verdictModerate, upgrade: 1,
		},
		{
			name: "a large effect does not raise randomized evidence",
			in: certaintyInput{Randomized: true, Checklist: yes(func(c *model.GradeChecklist) {
				c.LargeEffect = model.Yes
			})},
			want: verdictStrong,
		},
		{
			name: "the raise and a lowering cancel out",
			in: certaintyInput{Checklist: yes(func(c *model.GradeChecklist) {
				c.LargeEffect = model.Yes
				c.Indirectness.Intervention = model.Yes
			})},
			want: verdictWeak, domains: [5]int{0, 0, 1, 0, 0}, upgrade: 1,
		},
		{
			name: "the level is floored at insufficient",
			in: certaintyInput{Imprecision: imprecise(2), Checklist: yes(func(c *model.GradeChecklist) {
				c.RiskOfBias.PerStudy[0].Attrition = model.Yes
			})},
			want: verdictInsufficient, domains: [5]int{1, 0, 0, 2, 0},
		},
		{
			name: "every domain at once is floored too",
			in: certaintyInput{
				Randomized: true, BlindingFired: true, Comparable: 3, Imprecision: imprecise(2),
				Checklist: yes(func(c *model.GradeChecklist) {
					c.RiskOfBias.PerStudy[0].Attrition = model.Yes
					c.Inconsistency.DirectionsConflict = model.Yes
					c.Indirectness.Population = model.Yes
					c.PublicationBias.Suspected = model.Yes
				}),
			},
			want: verdictInsufficient, domains: [5]int{2, 1, 1, 2, 1},
		},
		{
			name: "no direct study is insufficient whatever the arithmetic gives",
			in:   certaintyInput{Randomized: true, NoDirectStudy: true, Checklist: noFlags()},
			want: verdictInsufficient,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			if in.Imprecision == nil && !tc.withoutPool {
				in.Imprecision = imprecise(0)
			}

			if in.Studies == nil {
				in.Studies = oneStudy()
			}

			got := computeGrade(in)

			assert.Equal(t, tc.want, got.Level)
			domains := [5]int{
				got.RiskOfBias.Levels, got.Inconsistency.Levels, got.Indirectness.Levels,
				got.Imprecision.Levels, got.PublicationBias.Levels,
			}

			assert.Equal(t, tc.domains, domains)
			assert.Equal(t, tc.upgrade, got.Upgrade)
			assert.Equal(t, tc.in.NoDirectStudy, got.NoDirectStudy)
			assert.Equal(t, tc.in.Checklist, got.Checklist, "the answers are stored with the result")

			wantStart := verdictWeak
			if tc.in.Randomized {
				wantStart = verdictStrong
			}

			assert.Equal(t, wantStart, got.Start)
		})
	}
}

func TestComputeGradeNamesTheCauses(t *testing.T) {
	c := noFlags()
	c.RiskOfBias.PerStudy[0].Attrition = model.Yes
	c.RiskOfBias.PerStudy[0].StoppedEarlyForBenefit = model.Yes
	c.Indirectness.Comparator = model.Yes
	c.Inconsistency.SizesDifferBeyondChance = model.Yes

	got := computeGrade(certaintyInput{
		Randomized: true, BlindingFired: true, Comparable: 2, Imprecision: &model.Imprecision{}, Checklist: c,
		Studies: oneStudy(),
	})

	assert.Equal(t, []string{model.CauseBlinding, model.CauseAttrition, model.CauseStoppedEarlyForBenefit},
		got.RiskOfBias.Causes)
	assert.Equal(t, 2, got.RiskOfBias.Levels, "three causes, two levels")
	assert.Equal(t, map[string]float64{
		model.CauseAttrition: 1, model.CauseAllocationFlaw: 0, model.CauseSelectiveOutcome: 0,
		model.CauseStoppedEarlyForBenefit: 1,
	}, got.RiskOfBias.Shares, "the share of every item is kept with the domain")
	assert.Equal(t, []string{"sizes_differ_beyond_chance"}, got.Inconsistency.Causes)
	assert.Equal(t, []string{"comparator"}, got.Indirectness.Causes)
	assert.Empty(t, got.Imprecision.Causes)
}

func TestImprecisionDomainClamps(t *testing.T) {
	assert.Equal(t, 2, imprecisionDomain(&model.Imprecision{Levels: 7}).Levels)
	assert.Equal(t, 0, imprecisionDomain(&model.Imprecision{Levels: -1}).Levels)
}

// Without a pooled estimate the rating is the model's imprecision_without_pool:
// its levels, clamped to 0..2, with no cause or reason of the pipeline's.
func TestImprecisionAnswerDomain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer *int
		levels int
	}{
		{"0", new(0), 0},
		{"1", new(1), 1},
		{"2", new(2), 2},
		{"above the scale", new(9), 2},
		{"below the scale", new(-1), 0},
		{"missing", nil, 0},
	} {
		d := imprecisionAnswerDomain(tc.answer)

		assert.Equal(t, tc.levels, d.Levels, tc.name)
		assert.Empty(t, d.Causes)
		assert.Empty(t, d.Shares)
	}
}

// directEvidence is an evidence base of the studies given.
func directEvidence(studies ...model.EvidenceStudy) *model.EvidenceBase {
	return &model.EvidenceBase{Direct: len(studies), Studies: studies}
}

func TestDirectPrimaryStudies(t *testing.T) {
	trial := model.EvidenceStudy{Design: model.DesignRCT, Participants: 60}
	cohort := model.EvidenceStudy{Design: model.DesignCohort, Participants: 30}
	unsized := model.EvidenceStudy{Design: model.DesignUnclear}
	review := model.EvidenceStudy{Design: model.DesignMetaAnalysis, Participants: 9000}
	narrative := model.EvidenceStudy{Design: model.DesignNarrativeReview, Participants: 500}

	eb := directEvidence(trial, review, cohort, narrative, unsized)

	assert.Len(t, directPrimaryStudies(eb), 3, "reviews are not primary studies")
	assert.Empty(t, directPrimaryStudies(nil))
}

func TestRandomizedStart(t *testing.T) {
	withRandomized := &model.EvidenceBase{Direct: 2, Randomized: &model.EvidenceGroup{Studies: 1}}
	observational := &model.EvidenceBase{Direct: 1, Observational: &model.EvidenceGroup{Studies: 1}}
	pooled := func(s model.Stratum) *model.PooledEstimate { return &model.PooledEstimate{Stratum: s} }

	for _, tc := range []struct {
		name string
		pe   *model.PooledEstimate
		eb   *model.EvidenceBase
		want bool
	}{
		{"a randomized pool starts strong", pooled(model.StratumRandomized), observational, true},
		{"a non-randomized pool starts weak even beside a trial", pooled(model.StratumNonRandomized), withRandomized, false},
		{"a pool without a stratum falls back on the direct studies: a trial", pooled(""), withRandomized, true},
		{"a pool without a stratum falls back on the direct studies: none", pooled(""), observational, false},
		{"without a pool any direct randomized trial starts strong", nil, withRandomized, true},
		{"without a pool, observational studies start weak", nil, observational, false},
		{"without the evidence base it starts weak", nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, randomizedStart(tc.pe, tc.eb))
		})
	}
}

// noDirectStudy reads the data only: no direct primary study report in the
// evidence base, whatever the model said.
func TestNoDirectStudy(t *testing.T) {
	trial := model.EvidenceStudy{Design: model.DesignRCT}
	review := model.EvidenceStudy{Design: model.DesignSystematicReview}

	assert.True(t, noDirectStudy(nil), "no evidence base: nothing direct")
	assert.True(t, noDirectStudy(&model.EvidenceBase{}))
	assert.True(t, noDirectStudy(directEvidence(review)), "a review alone is no direct primary study")
	assert.False(t, noDirectStudy(directEvidence(review, trial)))
	assert.False(t, noDirectStudy(directEvidence(model.EvidenceStudy{Design: model.DesignUnclear})),
		"a primary study of an unclear design still counts")
}

// The model's consensus_status insufficient_data does not make the level
// insufficient: a direct primary study is in the evidence.
func TestCertaintyIgnoresTheModelsConsensusStatus(t *testing.T) {
	p, _, _ := loggedRun(t)
	st := &State{}
	eb := directEvidence(model.EvidenceStudy{Design: model.DesignRCT, Participants: 500})
	eb.Randomized = &model.EvidenceGroup{Studies: 1}
	jr := &synthesisResult{Verdict: "strong", ConsensusStatus: consensusInsufficient}
	cl := noFlags()
	jr.GradeChecklist = &cl

	verdict, grade := p.certainty(st, eb, jr, nil, english())

	require.NotNil(t, grade)
	assert.False(t, grade.NoDirectStudy)
	assert.Equal(t, verdictStrong, grade.Start)
	assert.Equal(t, verdictModerate, verdict, "randomized start, the model's one level of imprecision without a pool")
	assert.Equal(t, 1, grade.Imprecision.Levels)

	noStudies, grade2 := p.certainty(st, directEvidence(model.EvidenceStudy{Design: model.DesignMetaAnalysis}), jr, nil,
		english())

	assert.True(t, grade2.NoDirectStudy)
	assert.Equal(t, verdictInsufficient, noStudies)
}

// checkChecklist reads the answer the way the retry path needs it: a missing
// or invalid checklist is rejected with errChecklist, a valid one is normalized.
func TestCheckChecklist(t *testing.T) {
	decode := func(t *testing.T, raw string) *synthesisResult {
		t.Helper()

		var jr synthesisResult

		require.NoError(t, json.Unmarshal([]byte(raw), &jr))

		return &jr
	}

	const rest = `"inconsistency":{"directions_conflict":"no","sizes_differ_beyond_chance":"no"},` +
		`"indirectness":{"population":"no","intervention":"no","comparator":"no","outcome":"no"},` +
		`"publication_bias":{"suspected":"no"}`

	t.Run("a valid checklist is normalized", func(t *testing.T) {
		jr := decode(t, `{"grade_checklist":{"risk_of_bias":{"per_study":[{"doi":" 10.1/a ","attrition":" YES ",`+
			`"allocation_flaw":"no","selective_outcome":"no","stopped_early_for_benefit":"no"}]},`+rest+`}}`)

		require.NoError(t, jr.checkChecklist(false))
		assert.Equal(t, model.Yes, jr.GradeChecklist.RiskOfBias.PerStudy[0].Attrition)
		assert.Equal(t, "10.1/a", jr.GradeChecklist.RiskOfBias.PerStudy[0].DOI)
		assert.Equal(t, model.No, jr.GradeChecklist.LargeEffect, "an omitted large_effect reads as no")
	})

	t.Run("the global risk-of-bias answers of an older reply are accepted", func(t *testing.T) {
		jr := decode(t, `{"grade_checklist":{"risk_of_bias":{"attrition":"yes","allocation_flaw":"no",`+
			`"selective_outcome":"no","stopped_early_for_benefit":"no","studies":["10.1/a"]},`+rest+`}}`)

		require.NoError(t, jr.checkChecklist(false))
		assert.Nil(t, jr.GradeChecklist.RiskOfBias.PerStudy)
		assert.Equal(t, model.Yes, jr.GradeChecklist.RiskOfBias.Attrition)
	})

	const perStudy = `"risk_of_bias":{"per_study":[]},` + rest

	t.Run("imprecision_without_pool is required without a pooled imprecision", func(t *testing.T) {
		missing := decode(t, `{"grade_checklist":{`+perStudy+`}}`)

		require.ErrorIs(t, missing.checkChecklist(true), errChecklist)
		require.ErrorContains(t, missing.checkChecklist(true), "imprecision_without_pool")

		given := decode(t, `{"grade_checklist":{`+perStudy+`,"imprecision_without_pool":2}}`)

		require.NoError(t, given.checkChecklist(true))
		require.NotNil(t, given.GradeChecklist.ImprecisionWithoutPool)
		assert.Equal(t, 2, *given.GradeChecklist.ImprecisionWithoutPool)

		outside := decode(t, `{"grade_checklist":{`+perStudy+`,"imprecision_without_pool":3}}`)

		require.ErrorIs(t, outside.checkChecklist(true), errChecklist)
	})

	t.Run("imprecision_without_pool is dropped when the pipeline computed the imprecision", func(t *testing.T) {
		jr := decode(t, `{"grade_checklist":{`+perStudy+`,"imprecision_without_pool":7}}`)

		require.NoError(t, jr.checkChecklist(false), "even a value outside 0..2 is no error then")
		assert.Nil(t, jr.GradeChecklist.ImprecisionWithoutPool)

		raw, err := json.Marshal(jr.GradeChecklist)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "imprecision_without_pool")
	})

	for name, raw := range map[string]string{
		"no checklist":       `{"verdict":"weak"}`,
		"a null checklist":   `{"grade_checklist":null}`,
		"an empty checklist": `{"grade_checklist":{}}`,
		"an invalid answer":  `{"grade_checklist":{"risk_of_bias":{"attrition":"maybe"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, decode(t, raw).checkChecklist(false), errChecklist)
		})
	}

	// A value of a wrong JSON type is an unusable checklist (the retry and the
	// fallback apply), never a decode error of the whole answer: each case is a
	// valid checklist with one piece replaced.
	const (
		valid     = `{"grade_checklist":{` + perStudy + `,"imprecision_without_pool":1}}`
		emptyList = `"per_study":[]`
		key       = `"imprecision_without_pool":` // what the cases below put a value after
	)

	for name, swap := range map[string][2]string{
		"a number as an answer":      {`"suspected":"no"`, `"suspected":2`},
		"an object as an answer":     {`"directions_conflict":"no"`, `"directions_conflict":{}`},
		"a list as an answer":        {`"population":"no"`, `"population":["yes"]`},
		"a null answer":              {`"outcome":"no"`, `"outcome":null`},
		"per_study that is no list":  {emptyList, `"per_study":"none"`},
		"an entry that is no object": {emptyList, `"per_study":["10.1/a"]`},
		"a list as risk_of_bias":     {`"risk_of_bias":{` + emptyList + `}`, `"risk_of_bias":[]`},
		"a number as a note":         {emptyList, emptyList + `,"note":5`},
		"a word as imprecision":      {key + `1`, key + `"high"`},
		"a fraction as imprecision":  {key + `1`, key + `1.5`},
		"a boolean as imprecision":   {key + `1`, key + `true`},
		"an object as imprecision":   {key + `1`, key + `{}`},
		"an imprecision above two":   {key + `1`, key + `"3"`},
		"a negative imprecision":     {key + `1`, key + `-1.0`},
		"a huge imprecision number":  {key + `1`, key + `1e30`},
		"an empty imprecision text":  {key + `1`, key + `""`},
		"a null imprecision":         {key + `1`, key + `null`},
	} {
		t.Run(name, func(t *testing.T) {
			raw := strings.Replace(valid, swap[0], swap[1], 1)
			require.NotEqual(t, valid, raw, "the swap must hit")

			require.ErrorIs(t, decode(t, raw).checkChecklist(true), errChecklist)
		})
	}

	require.NoError(t, decode(t, valid).checkChecklist(true), "the checklist the cases start from is valid")

	// the same wrong types are no problem where the value is not read
	t.Run("a wrong type of the dropped imprecision is no problem", func(t *testing.T) {
		jr := decode(t, `{"grade_checklist":{`+perStudy+`,"imprecision_without_pool":"high"}}`)

		require.NoError(t, jr.checkChecklist(false))
		assert.Nil(t, jr.GradeChecklist.ImprecisionWithoutPool)
	})

	t.Run("a boolean is yes or no, a whole number or its text is the imprecision", func(t *testing.T) {
		for _, imprecision := range []string{`2`, `2.0`, `"2"`, `" 2 "`, `"2.0"`, `2e0`} {
			jr := decode(t, `{"grade_checklist":{"risk_of_bias":{"per_study":[{"doi":"10.1/a","attrition":true,`+
				`"allocation_flaw":false,"selective_outcome":"no","stopped_early_for_benefit":"no"}]},`+
				`"inconsistency":{"directions_conflict":false,"sizes_differ_beyond_chance":false},`+
				`"indirectness":{"population":false,"intervention":false,"comparator":false,"outcome":false},`+
				`"publication_bias":{"suspected":true},"large_effect":false,`+
				`"imprecision_without_pool":`+imprecision+`}}`)

			require.NoError(t, jr.checkChecklist(true), imprecision)
			assert.Equal(t, model.Yes, jr.GradeChecklist.RiskOfBias.PerStudy[0].Attrition)
			assert.Equal(t, model.No, jr.GradeChecklist.RiskOfBias.PerStudy[0].AllocationFlaw)
			assert.Equal(t, model.Yes, jr.GradeChecklist.PublicationBias.Suspected)
			assert.Equal(t, model.No, jr.GradeChecklist.LargeEffect)
			require.NotNil(t, jr.GradeChecklist.ImprecisionWithoutPool, imprecision)
			assert.Equal(t, 2, *jr.GradeChecklist.ImprecisionWithoutPool, imprecision)
		}
	})

	t.Run("a per-study entry is repaired, not rejected", func(t *testing.T) {
		jr := decode(t, `{"grade_checklist":{"risk_of_bias":{"per_study":[`+
			`{"doi":"10.1/a","attrition":"yes"},`+
			`{"doi":"10.1/b","attrition":"no","allocation_flaw":"no","selective_outcome":"no",`+
			`"stopped_early_for_benefit":"maybe"},`+
			`{"doi":"10.1/c"}]},`+rest+`}}`)

		require.NoError(t, jr.checkChecklist(false))

		per := jr.GradeChecklist.RiskOfBias.PerStudy
		require.Len(t, per, 2, "the entry with an unknown word is left out")
		assert.Equal(t, "10.1/a", per[0].DOI)
		assert.Equal(t, model.Yes, per[0].Attrition)
		assert.Equal(t, model.No, per[0].StoppedEarlyForBenefit, "a missing item is no")
		assert.Equal(t, "10.1/c", per[1].DOI)
		assert.Equal(t, model.PerStudyRepairs{ItemsFilled: 3 + 4, EntriesDropped: 1},
			jr.GradeChecklist.RiskOfBias.Repairs)
	})
}

func TestAskJSONCheckedRetriesARejectedAnswer(t *testing.T) {
	good := synthesisReplyWith(t, "weak", noFlags())
	bad := `{"verdict":"weak","verdict_rationale":"stale","gaps":["stale gap"]}`

	t.Run("the retry names the problem and its answer replaces the first", func(t *testing.T) {
		llm := &echoCompleter{replies: []string{bad, good}}

		var jr synthesisResult

		usage, err := AskJSONChecked(t.Context(), llm, "sys", "task", &jr, 100,
			func() error { return jr.checkChecklist(true) })
		require.NoError(t, err)

		require.Len(t, llm.prompts, 2)
		assert.Contains(t, llm.prompts[1], "Your previous answer was rejected: ")
		assert.Contains(t, llm.prompts[1], "grade_checklist")
		assert.Contains(t, llm.prompts[1], "stale", "the rejected answer is quoted back")
		assert.Equal(t, 20, usage.InputTokens, "both calls are counted")
		assert.Equal(t, "r", jr.VerdictRationale)
		assert.Empty(t, jr.Gaps, "nothing of the rejected answer survives")
	})

	t.Run("a second rejection returns the checklist error with the second answer", func(t *testing.T) {
		llm := &echoCompleter{replies: []string{bad, `{"verdict":"moderate","verdict_rationale":"second"}`}}

		var jr synthesisResult

		_, err := AskJSONChecked(t.Context(), llm, "sys", "task", &jr, 100,
			func() error { return jr.checkChecklist(true) })
		require.ErrorIs(t, err, errChecklist)
		assert.Equal(t, 2, llm.calls)
		assert.Equal(t, "second", jr.VerdictRationale)
		assert.Empty(t, jr.Gaps)
	})

	t.Run("an accepted answer takes one call", func(t *testing.T) {
		llm := &echoCompleter{replies: []string{good}}

		var jr synthesisResult

		_, err := AskJSONChecked(t.Context(), llm, "sys", "task", &jr, 100,
			func() error { return jr.checkChecklist(true) })
		require.NoError(t, err)
		assert.Equal(t, 1, llm.calls)
	})

	t.Run("a broken answer keeps the old retry text", func(t *testing.T) {
		llm := &echoCompleter{replies: []string{`{"verdict":`, good}}

		var jr synthesisResult

		_, err := AskJSONChecked(t.Context(), llm, "sys", "task", &jr, 100,
			func() error { return jr.checkChecklist(true) })
		require.NoError(t, err)
		assert.Contains(t, llm.prompts[1], "Your previous answer was not valid JSON:")
	})

	t.Run("a first answer with a wrong-typed field leaves nothing behind for the retry", func(t *testing.T) {
		// the checklist decodes before "gaps" fails on its type: encoding/json keeps
		// the fields it could fill, so without a clean target the retry (no
		// checklist) would pass the check with the first answer's checklist
		wrongType := strings.Replace(good, `"consensus_status"`, `"gaps":"none","consensus_status"`, 1)
		require.NotEqual(t, good, wrongType)

		llm := &echoCompleter{replies: []string{wrongType, `{"verdict":"moderate","verdict_rationale":"second"}`}}

		var jr synthesisResult

		_, err := AskJSONChecked(t.Context(), llm, "sys", "task", &jr, 100,
			func() error { return jr.checkChecklist(true) })
		require.ErrorIs(t, err, errChecklist, "the retry's missing checklist is not filled from the first answer")
		assert.Contains(t, llm.prompts[1], "Your previous answer was not valid JSON:")
		assert.Nil(t, jr.GradeChecklist)
		assert.Equal(t, "second", jr.VerdictRationale)
	})

	t.Run("a second broken answer is an error, not a fallback", func(t *testing.T) {
		llm := &echoCompleter{replies: []string{`{"verdict":`, `nope`}}

		var jr synthesisResult

		_, err := AskJSONChecked(t.Context(), llm, "sys", "task", &jr, 100,
			func() error { return jr.checkChecklist(true) })
		require.Error(t, err)
		require.NotErrorIs(t, err, errChecklist)
	})

	// the first answer was valid JSON that only failed the check: when the retry
	// then fails altogether, the first answer is what the caller falls back on
	for name, llm := range map[string]*echoCompleter{
		"a broken retry keeps the first answer":      {replies: []string{bad, `{"gaps":[`}},
		"a failed retry call keeps the first answer": {replies: []string{bad}, failAt: 2},
	} {
		t.Run(name, func(t *testing.T) {
			var jr synthesisResult

			usage, err := AskJSONChecked(t.Context(), llm, "sys", "task", &jr, 100,
				func() error { return jr.checkChecklist(true) })
			require.ErrorIs(t, err, errChecklist, "the documented fallback applies")
			require.Error(t, err)
			assert.Equal(t, 2, llm.calls)
			assert.Positive(t, usage.InputTokens)
			assert.Equal(t, "stale", jr.VerdictRationale)
			assert.Equal(t, []string{"stale gap"}, jr.Gaps)
		})
	}

	t.Run("a refused retry still stops the run", func(t *testing.T) {
		llm := &stopCompleter{reply: func(call int, _ string) (string, error) {
			if call == 1 {
				return bad, nil
			}

			return "", fmt.Errorf("usage limit: %w", ErrLLMStop)
		}}

		var jr synthesisResult

		_, err := AskJSONChecked(t.Context(), llm, "sys", "task", &jr, 100,
			func() error { return jr.checkChecklist(true) })
		require.ErrorIs(t, err, ErrLLMStop)
	})
}

// A rejected first answer and a retry that is not JSON: the run goes on with
// the first answer's verdict, like with two rejected ones.
func TestRunFallsBackOnTheFirstAnswerWhenTheRetryIsBroken(t *testing.T) {
	first := `{"verdict":"weak","verdict_rationale":"first","consensus_status":"emerging"}`

	p, llm, logs := loggedRun(t, batchReply, first, "not json at all")

	st, _, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err, "a broken retry does not fail the run")

	assert.Equal(t, 3, llm.calls)
	assert.Equal(t, "weak", st.Report.Verdict)
	assert.Equal(t, "first", st.Report.VerdictRationale)
	assert.Nil(t, st.Report.Grade)
	assert.Contains(t, logs.String(), `msg="verdict kept from the synthesis: no usable grade_checklist"`)
}

// Without a usable checklist the model's verdict is kept, but never above what
// the data allow: no direct primary study is insufficient, a word outside the
// vocabulary becomes insufficient with a warning, a known one is lower-cased.
func TestFallbackVerdict(t *testing.T) {
	const noDirectLog = "no direct primary study" // what the log says

	trial := directEvidence(model.EvidenceStudy{Design: model.DesignRCT})
	review := directEvidence(model.EvidenceStudy{Design: model.DesignSystematicReview})

	for _, tc := range []struct {
		name string
		eb   *model.EvidenceBase
		said string
		want string
		warn string // the log says so; empty: no warning
	}{
		{"a direct study keeps the verdict", trial, verdictModerate, verdictModerate, ""},
		{"the case and the spaces are normalized", trial, " Strong ", verdictStrong, ""},
		{"no direct primary study is insufficient", review, verdictStrong, verdictInsufficient, noDirectLog},
		{"no evidence base is insufficient", nil, verdictModerate, verdictInsufficient, noDirectLog},
		{"insufficient needs no warning without studies", review, "INSUFFICIENT", verdictInsufficient, ""},
		{"an unknown word is insufficient", trial, "great", verdictInsufficient, "no known level"},
		{"an empty verdict is insufficient", trial, "", verdictInsufficient, "no known level"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, logs := loggedRun(t)

			assert.Equal(t, tc.want, p.fallbackVerdict(tc.eb, tc.said))

			if tc.warn == "" {
				assert.NotContains(t, logs.String(), "level=WARN")

				return
			}

			assert.Contains(t, logs.String(), "level=WARN")
			assert.Contains(t, logs.String(), tc.warn)
		})
	}
}

// A run without a usable checklist and without a direct primary study ends
// insufficient, whatever the model wrote.
func TestRunFallbackIsInsufficientWithoutADirectStudy(t *testing.T) {
	const batch = `{"critiques":[` +
		`{"doi":"10.1000/a","design_quality":"high","relevance":"direct","result_direction":"positive",` +
		`"study_design":"systematic_review"},` +
		`{"doi":"10.1000/b","design_quality":"low","relevance":"indirect","result_direction":"null"}]}`

	missing := `{"verdict":"strong","verdict_rationale":"no checklist","consensus_status":"emerging"}`

	p, _, logs := loggedRun(t, batch, missing, missing)

	st, _, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err)

	assert.Equal(t, "insufficient", st.Report.Verdict)
	assert.Nil(t, st.Report.Grade)
	assert.Contains(t, logs.String(), "verdict set to insufficient: no direct primary study")
}

// A computed level that is not the model's own is told to the reader, in the
// language of the report, with the model's level named; an agreeing verdict
// adds nothing.
func TestRunWarnsWhenTheComputedVerdictDiffers(t *testing.T) {
	for _, tc := range []struct {
		lang model.Language
		want string
	}{
		{model.LangEN, "The verdict is the certainty level computed from the grade checklist (moderate); " +
			"the model's own level was strong, so the rationale below may argue for that one."},
		{model.LangRU, "Вывод показан по уровню достоверности, рассчитанному по фактическим ответам модели " +
			"(умеренный); собственный уровень модели был другим (сильный), поэтому обоснование ниже может " +
			"отстаивать именно его."},
	} {
		t.Run(string(tc.lang), func(t *testing.T) {
			p, _, _ := loggedRun(t, batchReply, synthesisReplyWith(t, " Strong ", noFlags()))
			opts := runOptions(t)
			opts.Language = tc.lang

			st, dir, err := p.Run(t.Context(), opts)
			require.NoError(t, err)

			assert.Equal(t, "moderate", st.Report.Verdict)
			assert.Contains(t, st.Report.Warnings, tc.want)

			//nolint:gosec // reads a file the run just wrote into the test's temp directory
			report, err := os.ReadFile(filepath.Join(dir, "report.md"))
			require.NoError(t, err)
			assert.Contains(t, string(report), tc.want, "the report shows it")
		})
	}

	t.Run("an agreeing verdict adds no warning", func(t *testing.T) {
		p, _, _ := loggedRun(t, batchReply, synthesisReply)

		st, _, err := p.Run(t.Context(), runOptions(t))
		require.NoError(t, err)

		for _, w := range st.Report.Warnings {
			assert.NotContains(t, w, "the model's own level")
		}
	})

	t.Run("an empty verdict names no level", func(t *testing.T) {
		p, _, _ := loggedRun(t, batchReply, synthesisReplyWith(t, "", noFlags()))

		st, _, err := p.Run(t.Context(), runOptions(t))
		require.NoError(t, err)

		assert.Equal(t, "moderate", st.Report.Verdict)

		for _, w := range st.Report.Warnings {
			assert.NotContains(t, w, "the model's own level")
		}
	})
}

// loggedRun is runFakes with a log the test can read.
func loggedRun(t *testing.T, replies ...string) (*Pipeline, *scriptCompleter, *bytes.Buffer) {
	t.Helper()

	p, _, llm := runFakes(t, replies...)

	var logs bytes.Buffer

	p.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	return p, llm, &logs
}

func runOptions(t *testing.T) Options {
	t.Helper()

	return Options{
		DOIFile: doiList(t, listDOIA, listDOIB), RunDir: filepath.Join(t.TempDir(), "run"), TextMode: TextLean,
	}
}

// The code's level wins over the model's verdict, with a warning that records
// both; the rationale stays the model's; the report shows the arithmetic and
// the artifact keeps it.
func TestRunSetsTheVerdictFromTheChecklist(t *testing.T) {
	p, llm, logs := loggedRun(t, batchReply, synthesisReplyWith(t, "strong", noFlags()))

	st, dir, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err)

	require.NotNil(t, st.Report)
	assert.Equal(t, "moderate", st.Report.Verdict,
		"a randomized start, one level for the model's imprecision rating of a run without a pool")
	assert.Equal(t, "r", st.Report.VerdictRationale)
	assert.Equal(t, 2, llm.calls, "a valid checklist needs no retry")

	g := st.Report.Grade
	require.NotNil(t, g)
	assert.Equal(t, "moderate", g.Level)
	assert.Equal(t, "strong", g.Start)
	assert.Equal(t, noFlags(), g.Checklist)
	assert.Equal(t, 1, g.Imprecision.Levels)
	assert.NotContains(t, logs.String(), "global answers", "the per-study shape needs no fallback")

	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), `msg="verdict set from the grade checklist"`)
	assert.Contains(t, logs.String(), "computed=moderate")
	assert.Contains(t, logs.String(), "synthesis=strong")

	//nolint:gosec // reads a file the run just wrote into the test's temp directory
	report, err := os.ReadFile(filepath.Join(dir, "report.md"))
	require.NoError(t, err)
	assert.Contains(t, string(report), "## Verdict: MODERATE")
	assert.Contains(t, string(report), "**Certainty (computed):** start strong; imprecision −1; → moderate\n")

	loaded, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, st.Report.Grade, loaded.Report.Grade, "the arithmetic round-trips through the artifact")
	assert.Equal(t, string(report), RenderMarkdown(loaded))
}

// A verdict that agrees with the arithmetic raises no warning.
func TestRunKeepsAnAgreeingVerdictQuietly(t *testing.T) {
	p, _, logs := loggedRun(t, batchReply, synthesisReply)

	st, _, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err)

	assert.Equal(t, "moderate", st.Report.Verdict)
	assert.NotContains(t, logs.String(), "verdict set from the grade checklist")
	assert.NotContains(t, logs.String(), "no usable grade_checklist")
	assert.Zero(t, st.Report.Grade.Upgrade, "randomized evidence is not raised")
	assert.Equal(t, 1, st.Report.Grade.Imprecision.Levels)
}

// consensus_status insufficient_data in the model's reply does not decide the
// level: a direct primary study is in the evidence, so the arithmetic runs.
func TestRunIgnoresTheModelsInsufficientDataStatus(t *testing.T) {
	reply := `{"verdict":"insufficient","verdict_rationale":"r",` + gradeChecklistReply +
		`"consensus_status":"insufficient_data","consensus":"c"}`

	p, _, _ := loggedRun(t, batchReply, reply)

	st, _, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err)

	assert.Equal(t, "moderate", st.Report.Verdict)
	assert.False(t, st.Report.Grade.NoDirectStudy)
}

// A run whose only direct study is a review has no direct primary study: the
// level is insufficient whatever the model says.
func TestRunWithOnlyAReviewHasNoDirectStudy(t *testing.T) {
	const batch = `{"critiques":[` +
		`{"doi":"10.1000/a","design_quality":"high","relevance":"direct","result_direction":"positive",` +
		`"study_design":"systematic_review"},` +
		`{"doi":"10.1000/b","design_quality":"low","relevance":"indirect","result_direction":"null"}]}`

	p, _, _ := loggedRun(t, batch, synthesisReply)

	st, _, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err)

	assert.Equal(t, "insufficient", st.Report.Verdict)
	assert.True(t, st.Report.Grade.NoDirectStudy)
}

// pooledOpenBatch is two open-label randomized trials of a subjective outcome
// that pool into one estimate: the lack of blinding fires, a (1000 + 1000
// participants) takes about 46% of the pool weight and b (1100 + 1080) 54%.
const pooledOpenBatch = `{"critiques":[` +
	`{"doi":"10.1000/a","design_quality":"high","relevance":"direct","result_direction":"null",` +
	`"study_design":"rct","blinding":"open","effect":{"outcome":"pain","outcome_objective":false,` +
	`"intervention":{"events":100,"total":1000},"control":{"events":200,"total":1000}}},` +
	`{"doi":"10.1000/b","design_quality":"high","relevance":"direct","result_direction":"null",` +
	`"study_design":"rct","blinding":"open","effect":{"outcome":"pain","outcome_objective":false,` +
	`"intervention":{"events":120,"total":1100},"control":{"events":220,"total":1080}}}]}`

// The whole run with a randomized pool: the computed lack of blinding and the
// per-study attrition answers weighed by the pool's weights, which the report
// names with the share. An item counts only when the studies answering yes
// carry more than half of the pool weight, whatever their number.
func TestRunComputesRiskOfBiasFromBlindingAndTheChecklist(t *testing.T) {
	const (
		attrition = model.CauseAttrition
		blinding  = model.CauseBlinding
	)

	for _, tc := range []struct {
		name      string
		answers   []model.RiskOfBiasStudy
		levels    int
		causes    []string
		share     float64
		verdict   string
		certainty string
	}{
		{
			name:    "both studies report attrition: all the weight",
			answers: []model.RiskOfBiasStudy{studyAnswer(listDOIA, attrition), studyAnswer(listDOIB, attrition)},
			levels:  2, causes: []string{blinding, attrition}, share: 1, verdict: "weak",
			certainty: "start strong; risk of bias −2 (lack of blinding, attrition 100%); → weak",
		},
		{
			name:    "the heavier study alone carries more than half",
			answers: []model.RiskOfBiasStudy{studyAnswer(listDOIA), studyAnswer(listDOIB, attrition)},
			levels:  2, causes: []string{blinding, attrition}, share: 0.54, verdict: "weak",
			certainty: "start strong; risk of bias −2 (lack of blinding, attrition 54%); → weak",
		},
		{
			name:    "the lighter study alone does not count",
			answers: []model.RiskOfBiasStudy{studyAnswer(listDOIA, attrition), studyAnswer(listDOIB)},
			levels:  1, causes: []string{blinding}, share: 0.46, verdict: "moderate",
			certainty: "start strong; risk of bias −1 (lack of blinding); → moderate",
		},
		{
			name:    "a study left out counts as no",
			answers: []model.RiskOfBiasStudy{studyAnswer(listDOIA, attrition)},
			levels:  1, causes: []string{blinding}, share: 0.46, verdict: "moderate",
			certainty: "start strong; risk of bias −1 (lack of blinding); → moderate",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cl := noFlags()
			cl.RiskOfBias.PerStudy = tc.answers

			p, _, logs := loggedRun(t, pooledOpenBatch, synthesisReplyWith(t, tc.verdict, cl))

			st, dir, err := p.Run(t.Context(), runOptions(t))
			require.NoError(t, err)

			require.NotNil(t, st.BlindingBias)
			require.NotNil(t, st.Imprecision)
			require.Zero(t, st.Imprecision.Levels)
			assert.NotContains(t, logs.String(), "global answers")

			g := st.Report.Grade
			require.NotNil(t, g)
			assert.Equal(t, "strong", g.Start, "a randomized pool starts strong")
			assert.Equal(t, tc.levels, g.RiskOfBias.Levels)
			assert.Equal(t, tc.causes, g.RiskOfBias.Causes)
			assert.InDelta(t, tc.share, g.RiskOfBias.Shares[model.CauseAttrition], 0.006,
				"the share is the pool weight of the studies that answered yes, not their participants")
			assert.Equal(t, tc.verdict, st.Report.Verdict)

			//nolint:gosec // reads a file the run just wrote into the test's temp directory
			report, err := os.ReadFile(filepath.Join(dir, "report.md"))
			require.NoError(t, err)

			assert.Contains(t, string(report), "**Certainty (computed):** "+tc.certainty)
			assert.Less(t, bytes.Index(report, []byte("Risk of bias from lack of blinding (computed)")),
				bytes.Index(report, []byte("**Certainty (computed)")), "the line sits under the blinding line")

			loaded, err := LoadState(dir)
			require.NoError(t, err)
			assert.Equal(t, st.Report.Grade, loaded.Report.Grade, "the shares round-trip through the artifact")
		})
	}
}

// An old reply with the four global answers (or a model that ignores the new
// shape) still works: the global answers count as before, with a warning.
func TestRunFallsBackOnTheGlobalRiskOfBiasAnswers(t *testing.T) {
	cl := noFlags()
	cl.RiskOfBias = model.RiskOfBiasItems{
		Attrition: model.Yes, AllocationFlaw: model.No, SelectiveOutcome: model.No, StoppedEarlyForBenefit: model.No,
	}

	p, llm, logs := loggedRun(t, batchReply, synthesisReplyWith(t, "weak", cl))

	st, _, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err)

	assert.Equal(t, 2, llm.calls, "the old shape is no reason for a retry")

	g := st.Report.Grade
	require.NotNil(t, g)
	assert.Equal(t, model.GradeDomain{Levels: 1, Causes: []string{model.CauseAttrition}}, g.RiskOfBias, "no shares")
	assert.Equal(t, "weak", st.Report.Verdict, "strong start, one level for attrition and one for imprecision")
	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(),
		`msg="risk of bias read from the global answers: the reply has no risk_of_bias.per_study"`)
}

// Without a pooled estimate the model rates the imprecision, and a reply that
// leaves the rating out is retried: it is required exactly there.
func TestRunRetriesAMissingImprecisionRatingWithoutAPool(t *testing.T) {
	cl := noFlags()
	cl.ImprecisionWithoutPool = nil

	p, llm, _ := loggedRun(t, batchReply, synthesisReplyWith(t, "moderate", cl), synthesisReply)

	st, _, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err)

	assert.Equal(t, 3, llm.calls)
	assert.Contains(t, llm.prompts[2], "imprecision_without_pool: missing")
	assert.Equal(t, 1, st.Report.Grade.Imprecision.Levels)
}

// With a pooled estimate the model is not asked for the rating: a reply that
// carries it is read and the answer is dropped, a reply without it is fine.
func TestRunDropsTheImprecisionRatingWithAPool(t *testing.T) {
	cl := noFlags()
	cl.ImprecisionWithoutPool = new(2)

	p, llm, _ := loggedRun(t, pooledOpenBatch, synthesisReplyWith(t, "moderate", cl))

	st, _, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err)

	assert.Equal(t, 2, llm.calls)
	assert.Zero(t, st.Report.Grade.Imprecision.Levels, "the pooled rating, not the model's 2")
	assert.Nil(t, st.Report.Grade.Checklist.ImprecisionWithoutPool)
}

// An answer without the checklist is retried once; a good second answer is used.
func TestRunRetriesAMissingChecklist(t *testing.T) {
	missing := `{"verdict":"strong","verdict_rationale":"no checklist"}`

	p, llm, logs := loggedRun(t, batchReply, missing, synthesisReply)

	st, _, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err)

	assert.Equal(t, 3, llm.calls, "critique + synthesis + its retry")
	assert.Contains(t, llm.prompts[2], "Your previous answer was rejected: ")
	assert.Equal(t, 200, st.Cost.Synthesis.InputTokens, "both calls are paid for")

	require.NotNil(t, st.Report.Grade)
	assert.Equal(t, "10.1000/a leads", st.Report.VerdictRationale, "the retry's answer replaced the first")
	assert.NotContains(t, logs.String(), "no usable grade_checklist")
}

// An invalid value takes the same retry.
func TestRunRetriesAnInvalidChecklistValue(t *testing.T) {
	cl := noFlags()
	cl.Inconsistency.DirectionsConflict = "probably"

	p, llm, _ := loggedRun(t, batchReply, synthesisReplyWith(t, "moderate", cl), synthesisReply)

	st, _, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err)

	assert.Equal(t, 3, llm.calls)
	assert.Contains(t, llm.prompts[2], "inconsistency.directions_conflict: unknown answer \"probably\"")
	require.NotNil(t, st.Report.Grade)
	assert.Equal(t, "moderate", st.Report.Verdict)
}

// A per-study entry that is incomplete or has an unknown word is repaired, not
// retried: a missing item counts as no, an entry with an unknown word is left
// out (the study counts as no everywhere), and the log says how many.
func TestRunRepairsThePerStudyAnswers(t *testing.T) {
	const (
		attrition = model.CauseAttrition
		repaired  = `msg="risk of bias per_study repaired: `
	)

	for _, tc := range []struct {
		name    string
		answers string
		levels  int // risk of bias levels after the repair (the shares are of one study)
		filled  string
		dropped string
	}{
		{
			name:    "an item missing from an entry is no",
			answers: `[{"doi":"10.1000/a","attrition":"yes","allocation_flaw":"no","selective_outcome":"no"}]`,
			levels:  1, filled: "items_filled=1", dropped: "entries_dropped=0",
		},
		{
			name: "an entry with an unknown word is left out",
			answers: `[{"doi":"10.1000/a","attrition":"probably","allocation_flaw":"no",` +
				`"selective_outcome":"no","stopped_early_for_benefit":"no"}]`,
			levels: 0, filled: "items_filled=0", dropped: "entries_dropped=1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := `{"verdict":"moderate","verdict_rationale":"r","grade_checklist":{` +
				`"risk_of_bias":{"per_study":` + tc.answers + `},` +
				`"inconsistency":{"directions_conflict":"no","sizes_differ_beyond_chance":"no"},` +
				`"indirectness":{"population":"no","intervention":"no","comparator":"no","outcome":"no"},` +
				`"publication_bias":{"suspected":"no"},"imprecision_without_pool":1}}`

			p, llm, logs := loggedRun(t, batchReply, reply)

			st, _, err := p.Run(t.Context(), runOptions(t))
			require.NoError(t, err)

			assert.Equal(t, 2, llm.calls, "a repairable entry is no reason for a retry")
			require.NotNil(t, st.Report.Grade)
			assert.Equal(t, tc.levels, st.Report.Grade.RiskOfBias.Levels)

			if tc.levels > 0 {
				assert.Equal(t, []string{attrition}, st.Report.Grade.RiskOfBias.Causes)
			}

			assert.Contains(t, logs.String(), repaired)
			assert.Contains(t, logs.String(), tc.filled)
			assert.Contains(t, logs.String(), tc.dropped)
		})
	}
}

// When the retry has no checklist either, the run goes on with the model's
// verdict and a warning; the report has no computed line.
func TestRunFallsBackOnTheModelsVerdictWithoutAChecklist(t *testing.T) {
	missing := `{"verdict":"strong","verdict_rationale":"no checklist","consensus_status":"emerging"}`

	p, llm, logs := loggedRun(t, batchReply, missing, missing)

	st, dir, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err, "a missing checklist does not fail the run")

	assert.Equal(t, 3, llm.calls)
	assert.Equal(t, "strong", st.Report.Verdict)
	assert.Nil(t, st.Report.Grade)
	assert.Equal(t, 200, st.Cost.Synthesis.InputTokens, "the retry is paid for")

	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), `msg="verdict kept from the synthesis: no usable grade_checklist"`)

	//nolint:gosec // reads a file the run just wrote into the test's temp directory
	report, err := os.ReadFile(filepath.Join(dir, "report.md"))
	require.NoError(t, err)
	assert.Contains(t, string(report), "## Verdict: STRONG")
	assert.NotContains(t, string(report), "Certainty (computed)")
}

// A report written before the checklist loads and renders as it did: its
// verdict stays and there is no computed line.
func TestStateWithoutGradeLoads(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "4-synthesis.json"),
		[]byte(`{"question":"q","papers":[],"report":{"question":"q","generated_at":"2026-11-01T00:00:00Z",`+
			`"verdict":"weak","verdict_rationale":"r","evidence_matrix":[]}}`), 0o600))

	st, err := LoadState(dir)
	require.NoError(t, err)

	assert.Nil(t, st.Report.Grade)
	assert.Equal(t, "weak", st.Report.Verdict)

	text := RenderMarkdown(st)
	assert.Contains(t, text, "## Verdict: WEAK")
	assert.NotContains(t, text, "Certainty")
}

func TestGradeTextIsLocalized(t *testing.T) {
	g := &model.GradeAssessment{
		Start:       verdictStrong,
		RiskOfBias:  model.GradeDomain{Levels: 1, Causes: []string{model.CauseAttrition}},
		Imprecision: model.GradeDomain{Levels: 1},
		Level:       verdictModerate,
	}
	withShares := &model.GradeAssessment{
		Start: verdictStrong, Level: verdictWeak,
		RiskOfBias: model.GradeDomain{
			Levels: 2, Causes: []string{model.CauseBlinding, model.CauseAttrition},
			Shares: map[string]float64{model.CauseAttrition: 0.64, model.CauseAllocationFlaw: 0.2},
		},
		Imprecision: model.GradeDomain{Levels: 1},
	}

	for _, tc := range []struct {
		name string
		lang model.Language
		g    *model.GradeAssessment
		want string
	}{
		{
			"en", model.LangEN, g,
			"start strong; risk of bias −1 (attrition), imprecision −1; → moderate",
		},
		{
			"ru", model.LangRU, g,
			"исходный уровень сильный; риск систематической ошибки −1 (выбывание участников), неточность −1; → умеренный",
		},
		{
			"en: risk of bias with the share of the weight behind each item", model.LangEN, withShares,
			"start strong; risk of bias −2 (lack of blinding, attrition 64%), imprecision −1; → weak",
		},
		{
			"ru: risk of bias with the share of the weight behind each item", model.LangRU, withShares,
			"исходный уровень сильный; риск систематической ошибки −2 " +
				"(отсутствие ослепления, выбывание участников 64%), неточность −1; → слабый",
		},
		{
			"en: a share is rounded to whole percent", model.LangEN,
			&model.GradeAssessment{
				Start: verdictStrong, Level: verdictModerate,
				RiskOfBias: model.GradeDomain{
					Levels: 1, Causes: []string{model.CauseSelectiveOutcome},
					Shares: map[string]float64{model.CauseSelectiveOutcome: 0.5149, model.CauseAttrition: 0.2},
				},
			},
			"start strong; risk of bias −1 (selective outcome 51%); → moderate",
		},
		{
			"en: nothing lowers it", model.LangEN,
			&model.GradeAssessment{Start: verdictStrong, Level: verdictStrong},
			"start strong; no downgrades; → strong",
		},
		{
			"en: upgrade and two causes", model.LangEN,
			&model.GradeAssessment{
				Start: verdictWeak, Upgrade: 1, Level: verdictWeak,
				Indirectness: model.GradeDomain{
					Levels: 1, Causes: []string{model.CausePopulation, model.CauseOutcome},
				},
			},
			"start weak; indirectness −1 (population, outcome), large effect +1; → weak",
		},
		{
			"ru: no direct study", model.LangRU,
			&model.GradeAssessment{Start: verdictStrong, NoDirectStudy: true, Level: verdictInsufficient},
			"нет прямых первичных исследований по исходу вопроса; → недостаточный",
		},
		{
			"en: no direct study", model.LangEN,
			&model.GradeAssessment{Start: verdictStrong, NoDirectStudy: true, Level: verdictInsufficient},
			"no direct primary study reporting the outcome; → insufficient",
		},
		{
			"ru: indirectness and selective outcome", model.LangRU,
			&model.GradeAssessment{
				Start: verdictStrong, Level: verdictWeak,
				RiskOfBias:   model.GradeDomain{Levels: 1, Causes: []string{model.CauseSelectiveOutcome}},
				Indirectness: model.GradeDomain{Levels: 1, Causes: []string{model.CausePopulation}},
			},
			"исходный уровень сильный; риск систематической ошибки −1 (избирательная публикация результатов), " +
				"непрямой характер доказательств −1 (популяция); → слабый",
		},
		{
			"en: a cause outside the table is shown as stored", model.LangEN,
			&model.GradeAssessment{
				Start: verdictStrong, Level: verdictModerate,
				RiskOfBias: model.GradeDomain{Levels: 1, Causes: []string{"new_cause"}},
			},
			"start strong; risk of bias −1 (new_cause); → moderate",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, lexiconFor(tc.lang).gradeText(tc.g))
		})
	}
}

// Every cause and level has a word in every language.
func TestGradeLexiconIsComplete(t *testing.T) {
	causes := []string{
		model.CauseBlinding, model.CauseAttrition, model.CauseAllocationFlaw, model.CauseSelectiveOutcome,
		model.CauseStoppedEarlyForBenefit, model.CauseDirectionsConflict, model.CauseSizesDiffer,
		model.CausePopulation, model.CauseIntervention, model.CauseComparator, model.CauseOutcome,
	}

	for _, lx := range []lexicon{english(), russian()} {
		for _, c := range causes {
			assert.NotEmpty(t, lx.grade.causes[c], "%s: %s", lx.lang, c)
		}

		for _, l := range certaintyNames {
			assert.NotEmpty(t, lx.grade.levels[l], "%s: %s", lx.lang, l)
		}
	}
}

// The prompt describes the checklist, its items and the arithmetic.
func TestSynthesisPromptCarriesTheChecklist(t *testing.T) {
	for _, want := range []string{
		`grade_checklist (object, REQUIRED)`,
		`"attrition": "yes"|"no"`, `"allocation_flaw"`, `"selective_outcome"`, `"stopped_early_for_benefit"`,
		`"directions_conflict"`, `"sizes_differ_beyond_chance"`,
		`"population": "yes"|"no"`, `"intervention"`, `"comparator"`, `"outcome"`,
		`"publication_bias": {"suspected"`, `"large_effect": "yes"|"no"`,
		"You do not judge how serious a problem is",
		"Apply exactly this arithmetic",
		"strong = 3, moderate = 2, weak = 1, insufficient = 0",
		"loss to follow-up above 20% in any arm, or a difference between the arms above 10 percentage points",
		"quasi-random or alternating allocation, allocation by date or record number",
		"the text states that allocation was not random or not concealed",
		"A baseline imbalance between the arms (in age, prognostic factors or any other characteristic) is NOT " +
			"this item",
		"the outcome is a post hoc, non-prespecified or subgroup-only analysis",
		"Answer \"yes\" ONLY when the text states it",
		"an unreported detail is a gap, not a flaw",
		"Lack of blinding is not an item",
		"do not count blinding again",
		"two levels at most",
		"a population narrower than the question or a different setting are \"no\" by themselves",
		`"per_study": [{"doi": doi, "attrition": "yes"|"no"`,
		"has one entry for EVERY direct primary study the input lists",
		"with its doi exactly as the input gives it",
		"do not choose which studies matter and do not skip a small one",
		"ignores a doi that is not a direct primary study",
		`counts a study you leave out as "no" on every item`,
		"Use [] when there is no such study",
		"An item counts when the studies you answered \"yes\" for carry more than half of the weight",
		"the pooled weights when pooled_estimate is present, otherwise the participants",
		`"imprecision_without_pool": 0|1|2`,
		`required only when the input has no "imprecision" field`,
		"When the input has no imprecision field, give your own rating in imprecision_without_pool (0, 1 or 2 levels)",
		"(for a study the input lists without a DOI, put its title in the doi field)",
		"judged from the confidence interval and the amount of data, not from the number of studies",
		"Serious (1) when the interval includes no effect, or includes both an important and a trivial effect",
		"serious when the data are small (under about 300 events, or 400 participants for a continuous outcome)",
		"Very serious (2) when all the direct data are a few dozen participants (under about 100 in total)",
		"reaches well into both a clear benefit and a clear harm (e.g. RR 0.5 to 2)",
		"a narrow one that excludes no effect is precise, however few the trials",
		"one adequately sized trial with a clear result is not rated down",
		"the pipeline decides this from the studies, not from your consensus_status",
		"with fewer than 2 comparable studies answer \"no\" to both",
		"one level when large_effect is \"yes\" and the start is weak",
		"The number of studies and of research groups is not a domain",
		"No effect is a conclusion, not the lack of one",
	} {
		assert.Contains(t, synthesisSystemPrompt, want)
	}

	assert.NotContains(t, synthesisSystemPrompt, "Lower one level for each SERIOUS problem")
	assert.NotContains(t, synthesisSystemPrompt, "imprecision_no_pool", "the pipeline computes no imprecision itself")
	assert.NotContains(t, synthesisSystemPrompt, "Answer every checklist item on the studies that carry most",
		"the model no longer decides which studies carry the evidence for risk of bias")
	assert.NotContains(t, synthesisSystemPrompt, `"studies": [doi, ...]`)
	assert.NotContains(t, synthesisSystemPrompt, "or an important baseline imbalance the authors report")
	assert.Contains(t, synthesisPrompt(model.LangRU), "grade_checklist (object, REQUIRED)")
}
