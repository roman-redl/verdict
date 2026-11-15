package pipeline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// The share of the pool's weight carried by studies of a related outcome: above
// one half rates the outcome down, one half exactly does not. Without a pooled
// estimate that has weights there is no share.
func TestRelatedOutcomesShare(t *testing.T) {
	weighted := &model.PooledEstimate{Studies: []string{"a", "b"}, Weights: []float64{0.5, 0.5}}

	study := func(weight, related float64) evidenceStudy {
		return evidenceStudy{design: model.DesignRCT, weight: weight, relatedWeight: related}
	}

	for _, tc := range []struct {
		name      string
		studies   []evidenceStudy
		pe        *model.PooledEstimate
		want      *model.RelatedOutcomes
		wantDelta float64
	}{
		{
			name:    "no related outcome",
			studies: []evidenceStudy{study(0.6, 0), study(0.4, 0)},
			pe:      weighted, want: &model.RelatedOutcomes{Share: 0},
		},
		{
			name:    "a share below one half",
			studies: []evidenceStudy{study(0.6, 0), study(0.4, 0.4)},
			pe:      weighted, want: &model.RelatedOutcomes{Share: 0.4},
		},
		{
			name:    "exactly one half is not more than half",
			studies: []evidenceStudy{study(0.5, 0), study(0.5, 0.5)},
			pe:      weighted, want: &model.RelatedOutcomes{Share: 0.5},
		},
		{
			name:    "just above one half",
			studies: []evidenceStudy{study(0.49, 0), study(0.51, 0.51)},
			pe:      weighted, want: &model.RelatedOutcomes{Share: 0.51, RatedDown: true},
		},
		{
			name:    "every study of a related outcome",
			studies: []evidenceStudy{study(0.5, 0.5), study(0.5, 0.5)},
			pe:      weighted, want: &model.RelatedOutcomes{Share: 1, RatedDown: true},
		},
		{
			name:    "a review carries no part of it",
			studies: []evidenceStudy{study(1, 0), {design: model.DesignMetaAnalysis, weight: 1, relatedWeight: 1}},
			pe:      weighted, want: &model.RelatedOutcomes{Share: 0},
		},
		{
			name:    "the shares are of the weights, whatever their sum",
			studies: []evidenceStudy{study(2, 0), study(6, 6)},
			pe:      weighted, want: &model.RelatedOutcomes{Share: 0.75, RatedDown: true},
		},
		{name: "no pooled estimate", studies: []evidenceStudy{study(1, 1)}},
		{
			name:    "a pooled estimate without weights",
			studies: []evidenceStudy{study(1, 1)},
			pe:      &model.PooledEstimate{Studies: []string{"a", "b"}},
		},
		{
			name:    "no study carries the weights",
			studies: []evidenceStudy{study(0, 0)},
			pe:      weighted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := relatedOutcomes(tc.studies, tc.pe)

			if tc.want == nil {
				assert.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			assert.InDelta(t, tc.want.Share, got.Share, 1e-9)
			assert.Equal(t, tc.want.RatedDown, got.RatedDown)
		})
	}
}

// The pool takes a related outcome and a different one stays out; the share is
// the related study's weight in the estimate, and the study is marked in the
// evidence base. The pooled RR is the same in all the studies, so the weights
// follow the sizes: 2 to 1.
func TestPoolWeighsTheRelatedOutcome(t *testing.T) {
	// a fresh effect each time: matched labels the effect it is given
	big := func() *model.Effect { return counts(20, 200, 40, 200) }
	small := func() *model.Effect { return counts(10, 100, 20, 100) }

	for _, tc := range []struct {
		name           string
		same, relative func() *model.Effect
		wantShare      float64
		wantRated      bool
	}{
		{name: "the related study carries two thirds", same: small, relative: big, wantShare: 2.0 / 3, wantRated: true},
		{name: "the related study carries one third", same: big, relative: small, wantShare: 1.0 / 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			st := poolCorpus(
				poolStudy{doi: trialA, design: model.DesignRCT, effect: tc.same()},
				poolStudy{
					doi: trialB, design: model.DesignRCT,
					effect: matched(tc.relative(), model.OutcomeRelated, model.TimepointYes, 0),
				},
				poolStudy{
					doi: trialC, design: model.DesignRCT,
					effect: matched(counts(11, 100, 21, 100), model.OutcomeDifferent, model.TimepointYes, 0),
				},
			)

			res := p.pool(st)

			require.NotNil(t, res.est)
			assert.Equal(t, []string{trialA, trialB}, res.est.Studies)
			assert.Equal(t, []string{trialC + " (other outcome)"}, res.est.LeftOut)

			st.Pooled = res.est
			got := p.assessRelatedOutcomes(st)

			require.NotNil(t, got)
			assert.InDelta(t, tc.wantShare, got.Share, 1e-6)
			assert.InDelta(t, res.est.Weights[1], got.Share, 1e-9, "the share is the related study's weight in the pool")
			assert.Equal(t, tc.wantRated, got.RatedDown)

			eb := buildEvidenceBase(st, st.Pooled)
			require.NotNil(t, eb)

			related := map[string]bool{}
			for _, s := range eb.Studies {
				related[s.DOI] = s.OutcomeRelated
			}

			assert.Equal(t, map[string]bool{trialA: false, trialB: true, trialC: false}, related,
				"only a pooled study of a related outcome is marked")
		})
	}
}

// A pool of one study has weight 1: a single related study is all of it.
func TestRelatedOutcomesOfASingleStudy(t *testing.T) {
	p, _ := poolPipeline(t)

	st := poolCorpus(poolStudy{
		doi: trialA, design: model.DesignRCT,
		effect: matched(counts(10, 100, 20, 100), model.OutcomeRelated, model.TimepointYes, 0),
	})
	st.Pooled = p.pool(st).est

	require.NotNil(t, st.Pooled)
	require.Equal(t, model.PoolSingleStudy, st.Pooled.Method)

	got := p.assessRelatedOutcomes(st)
	require.NotNil(t, got)
	assert.InDelta(t, 1.0, got.Share, 1e-9)
	assert.True(t, got.RatedDown)
}

// Without a pooled estimate there is no share: the outcome is the model's
// answer alone.
func TestNoRelatedOutcomesWithoutAPool(t *testing.T) {
	p, _ := poolPipeline(t)

	st := poolCorpus(poolStudy{doi: trialA, design: model.DesignRCT})
	assert.Nil(t, p.assessRelatedOutcomes(st))
}

// The indirectness domain: the model's answers, plus the outcome when related
// outcomes carry most of the pool's weight; one level at most; the share is
// stored whenever it is known.
func TestIndirectnessDomainFromTheRelatedShare(t *testing.T) {
	const outcome = model.CauseOutcome

	answers := func(mod func(it *model.IndirectnessItems)) model.IndirectnessItems {
		it := noFlags().Indirectness
		mod(&it)

		return it
	}
	noAnswer := answers(func(*model.IndirectnessItems) {})
	outcomeYes := answers(func(it *model.IndirectnessItems) { it.Outcome = model.Yes })
	populationYes := answers(func(it *model.IndirectnessItems) { it.Population = model.Yes })

	for _, tc := range []struct {
		name       string
		related    *model.RelatedOutcomes
		answers    model.IndirectnessItems
		wantLevels int
		wantCauses []string
		wantShare  *float64
	}{
		{
			name:    "below one half and no answer: nothing, the share is kept",
			related: &model.RelatedOutcomes{Share: 0.4}, answers: noAnswer,
			wantShare: new(0.4),
		},
		{
			name:    "exactly one half is not rated",
			related: &model.RelatedOutcomes{Share: 0.5}, answers: noAnswer,
			wantShare: new(0.5),
		},
		{
			name:    "above one half is the outcome",
			related: &model.RelatedOutcomes{Share: 0.51, RatedDown: true}, answers: noAnswer,
			wantLevels: 1, wantCauses: []string{outcome}, wantShare: new(0.51),
		},
		{
			name:    "the model's outcome yes counts below one half too (an OR)",
			related: &model.RelatedOutcomes{Share: 0.4}, answers: outcomeYes,
			wantLevels: 1, wantCauses: []string{outcome}, wantShare: new(0.4),
		},
		{
			name:    "both say outcome: one cause, one level",
			related: &model.RelatedOutcomes{Share: 0.9, RatedDown: true}, answers: outcomeYes,
			wantLevels: 1, wantCauses: []string{outcome}, wantShare: new(0.9),
		},
		{
			name:    "another element and the share: two causes, still one level",
			related: &model.RelatedOutcomes{Share: 0.9, RatedDown: true}, answers: populationYes,
			wantLevels: 1, wantCauses: []string{model.CausePopulation, outcome}, wantShare: new(0.9),
		},
		{
			name:    "no pooled estimate and no answer: unchanged",
			related: nil, answers: noAnswer,
		},
		{
			name:    "no pooled estimate: the model's outcome yes alone counts",
			related: nil, answers: outcomeYes,
			wantLevels: 1, wantCauses: []string{outcome},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := indirectnessDomain(tc.related, &tc.answers)

			assert.Equal(t, tc.wantLevels, d.Levels)
			assert.Equal(t, tc.wantCauses, d.Causes)

			if tc.wantShare == nil {
				assert.Nil(t, d.Shares)
				return
			}

			assert.Equal(t, map[string]float64{outcome: *tc.wantShare}, d.Shares)
		})
	}
}

// The share reaches the level: one level less than the same answers without it.
func TestComputeGradeLowersByTheRelatedShare(t *testing.T) {
	in := certaintyInput{Randomized: true, Imprecision: &model.Imprecision{}, Checklist: noFlags(), Studies: oneStudy()}

	base := computeGrade(in)
	assert.Equal(t, verdictStrong, base.Level)
	assert.Nil(t, base.Indirectness.Shares)

	in.Related = &model.RelatedOutcomes{Share: 0.7, RatedDown: true}

	got := computeGrade(in)
	assert.Equal(t, verdictModerate, got.Level)
	assert.Equal(t, 1, got.Indirectness.Levels)
	assert.Equal(t, []string{model.CauseOutcome}, got.Indirectness.Causes)
	assert.InDelta(t, 0.7, got.Indirectness.Shares[model.CauseOutcome], 1e-9)
}

// The synthesis input carries the related share when there is one, and the
// prompt tells the judge to leave related outcomes to the pipeline.
func TestSynthesisInputCarriesTheRelatedOutcomes(t *testing.T) {
	st := poolState(counts(100, 1000, 200, 1000))

	without, err := synthesisUserPrompt(st)
	require.NoError(t, err)
	assert.NotContains(t, without, "related_outcomes")

	st.RelatedOutcomes = &model.RelatedOutcomes{Share: 0.62, RatedDown: true}

	with, err := synthesisUserPrompt(st)
	require.NoError(t, err)
	assert.Contains(t, with, `"related_outcomes":{"share":0.62,"rated_down":true}`)

	assert.Contains(t, synthesisSystemPrompt, "When the input has related_outcomes")
	assert.Contains(t, synthesisSystemPrompt, "do not count those again")
	assert.Contains(t, synthesisSystemPrompt, "The pool takes studies of the question's outcome and of a related one")
}

// The wording of the computed line: the share of the related outcomes is shown
// when it is the reason (above one half), not when only the model's answer is.
func TestDomainTextShowsTheRelatedShareOnlyWhenItCounts(t *testing.T) {
	counting := model.GradeDomain{
		Levels: 1, Causes: []string{model.CauseOutcome}, Shares: map[string]float64{model.CauseOutcome: 0.62},
	}
	modelOnly := model.GradeDomain{
		Levels: 1, Causes: []string{model.CauseOutcome}, Shares: map[string]float64{model.CauseOutcome: 0.4},
	}

	assert.Equal(t, "indirectness −1 (outcome 62%)", english().domainText("indirectness", counting))
	assert.Equal(t, "indirectness −1 (outcome)", english().domainText("indirectness", modelOnly))
	assert.Equal(t, "непрямой характер доказательств −1 (исход 62%)",
		russian().domainText("непрямой характер доказательств", counting))
}

// The list of studies marks a pooled study of a related outcome, in both
// languages, and no other.
func TestStudyListMarksTheRelatedOutcome(t *testing.T) {
	eb := &model.EvidenceBase{Studies: []model.EvidenceStudy{
		{
			Title: "Trial of the outcome", Year: 2020, Design: model.DesignRCT, Participants: 400,
			Direction: model.DirectionSupports, Pooled: true,
		},
		{
			Title: "Trial of a related outcome", Year: 2021, Design: model.DesignRCT, Participants: 300,
			Direction: model.DirectionSupports, Pooled: true, OutcomeRelated: true,
		},
		{
			Title: "Trial not pooled", Year: 2022, Design: model.DesignRCT, Participants: 200,
			Direction: model.DirectionNull,
		},
	}}

	en := english().studyList(eb)
	assert.Contains(t, en, listSep+"pooled, related outcome\n")
	assert.Equal(t, 1, strings.Count(en, ", related outcome"))
	assert.Equal(t, 2, strings.Count(en, listSep+"pooled"))

	ru := russian().studyList(eb)
	assert.Contains(t, ru, listSep+"в объединённой оценке, смежный исход\n")
	assert.Equal(t, 1, strings.Count(ru, ", смежный исход"))
	assert.Equal(t, 2, strings.Count(ru, listSep+"в объединённой оценке"))
}

// A critique and an effect of an older run carry the keys of the removed
// outcome vote: they load, and what the vote kept is simply not read.
func TestOldCritiqueWithTheVoteKeysLoads(t *testing.T) {
	const raw = `{"doi":"10.1/a","relevance":"direct","effect_asked":true,"match_adjudicated":true,` +
		`"effect":{"outcome":"x","outcome_match":"related","outcome_match_critic":"same",` +
		`"timepoint_match":"no","timepoint_match_critic":"yes",` +
		`"intervention":{"events":3,"total":50},"control":{"events":9,"total":50}}}`

	var c model.Critique
	require.NoError(t, json.Unmarshal([]byte(raw), &c))

	assert.True(t, c.EffectAsked)
	require.NotNil(t, c.Effect)
	assert.Equal(t, model.OutcomeRelated, c.Effect.OutcomeMatch)
	assert.Equal(t, model.TimepointNo, c.Effect.TimepointMatch)

	b, err := json.Marshal(c)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "match_adjudicated")
	assert.NotContains(t, string(b), "_critic")
}

// pooledRelatedBatch is two randomized trials of a related outcome (the
// question's outcome is another one) that pool into one estimate.
const pooledRelatedBatch = `{"critiques":[` +
	`{"doi":"10.1000/a","design_quality":"high","relevance":"direct","result_direction":"positive",` +
	`"study_design":"rct","effect":{"outcome":"need for ventilation","outcome_match":"related",` +
	`"intervention":{"events":100,"total":1000},"control":{"events":200,"total":1000}}},` +
	`{"doi":"10.1000/b","design_quality":"high","relevance":"direct","result_direction":"positive",` +
	`"study_design":"rct","effect":{"outcome":"need for ventilation","outcome_match":"related",` +
	`"intervention":{"events":120,"total":1100},"control":{"events":220,"total":1080}}}]}`

// The whole run: the studies of a related outcome are pooled, the share goes to
// the synthesis input, the indirectness domain counts the outcome and stores the
// share, and the report names it and marks the studies.
func TestRunPoolsRelatedOutcomesAndRatesIndirectness(t *testing.T) {
	p, llm, _ := loggedRun(t, pooledRelatedBatch, synthesisReplyWith(t, "strong", noFlags()))

	st, dir, err := p.Run(t.Context(), runOptions(t))
	require.NoError(t, err)

	require.NotNil(t, st.Pooled)
	assert.Equal(t, 2, st.Pooled.K, "both related studies are pooled")
	assert.Empty(t, st.Pooled.LeftOut)

	require.NotNil(t, st.RelatedOutcomes)
	assert.InDelta(t, 1.0, st.RelatedOutcomes.Share, 1e-9)
	assert.True(t, st.RelatedOutcomes.RatedDown)
	assert.Contains(t, llm.prompts[1], `"related_outcomes":{"share":1,"rated_down":true}`)

	g := st.Report.Grade
	require.NotNil(t, g)
	assert.Equal(t, 1, g.Indirectness.Levels, "the model answered no: the pipeline counted the outcome")
	assert.Equal(t, []string{model.CauseOutcome}, g.Indirectness.Causes)
	assert.InDelta(t, 1.0, g.Indirectness.Shares[model.CauseOutcome], 1e-9)
	assert.Equal(t, "moderate", g.Level)

	md := RenderMarkdown(st)
	assert.Contains(t, md, "indirectness −1 (outcome 100%)")
	assert.Contains(t, md, "pooled, related outcome")

	loaded, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, st.RelatedOutcomes, loaded.RelatedOutcomes)
}
