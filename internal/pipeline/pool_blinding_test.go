package pipeline

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	doiW1 = "10.1/w1"
	doiW2 = "10.1/w2"
	doiW3 = "10.1/w3"
)

// withEvents states the total of events the critic copied for a study.
func withEvents(ef *model.Effect, total float64) *model.Effect {
	ef.EventsTotal = new(total)
	return ef
}

// withObjective states whether the outcome is objective (the critic's label).
func withObjective(ef *model.Effect, objective bool) *model.Effect {
	ef.OutcomeObjective = new(objective)
	return ef
}

func TestPoolEventsOfTheStudies(t *testing.T) {
	percentArms := &model.Effect{
		Intervention: &model.Arm{Percent: new(10.0), Total: new(100.0)},
		Control:      &model.Arm{Percent: new(20.0), Total: new(100.0)},
	}

	for _, tc := range []struct {
		name    string
		effects []*model.Effect
		want    int
	}{
		{
			name:    "the arms' counts of every study add up",
			effects: []*model.Effect{counts(10, 100, 20, 100), counts(20, 200, 40, 200)},
			want:    90,
		},
		{
			name: "a reported ratio gives the total the critic copied",
			effects: []*model.Effect{
				withEvents(reported("HR", 0.8, 0.6, 1.05), 150), withEvents(reported("HR", 0.7, 0.5, 0.98), 100),
			},
			want: 250,
		},
		{
			name:    "arm counts and a copied total mix in one pool",
			effects: []*model.Effect{counts(10, 100, 20, 100), withEvents(reported("RR", 0.8, 0.6, 1.05), 80)},
			want:    110,
		},
		{
			name:    "one study without a count makes the total unknown",
			effects: []*model.Effect{withEvents(reported("HR", 0.8, 0.6, 1.05), 150), reported("HR", 0.7, 0.5, 0.98)},
			want:    0,
		},
		{
			name:    "the arms win over a copied total",
			effects: []*model.Effect{withEvents(counts(10, 100, 20, 100), 999)},
			want:    30,
		},
		{name: "counts derived from percentages of the arms", effects: []*model.Effect{percentArms}, want: 30},
		{name: "a continuous outcome has no events", effects: []*model.Effect{means(2, unitPoints)}, want: 0},
		{
			name:    "a total that cannot be a count is unknown",
			effects: []*model.Effect{withEvents(reported("HR", 0.8, 0.6, 1.05), -5)},
			want:    0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			pe := p.poolEffects(poolState(tc.effects...))
			require.NotNil(t, pe)
			assert.Equal(t, tc.want, pe.Events)
		})
	}
}

// Events belong to a ratio pool only: a unified SMD pool of counts and means,
// and a pool of mean differences, show none, whatever the critic copied.
func TestPoolEventsOnlyForRatioPools(t *testing.T) {
	p, _ := poolPipeline(t)

	unified := p.poolEffects(poolState(
		withHigherBetter(means(2, unitPoints), true),
		withEvents(withEventHarm(counts(10, 100, 20, 100), true), 500),
	))
	require.NotNil(t, unified)
	assert.Equal(t, model.MeasureSMD, unified.Measure)
	assert.True(t, unified.Converted)
	assert.Zero(t, unified.Events)
	assert.NotContains(t, pooledText(unified), "events")
	assert.NotContains(t, pooledLine(unified), "events")

	md := p.poolEffects(poolState(withEvents(means(2, unitPoints), 40), shifted(withEvents(means(2, unitPoints), 40))))
	require.NotNil(t, md)
	assert.Equal(t, model.MeasureMD, md.Measure)
	assert.Zero(t, md.Events)
	assert.NotContains(t, pooledText(md), "events")

	// the same counts in a ratio pool are shown
	ratio := p.poolEffects(poolState(withEvents(counts(10, 100, 20, 100), 500)))
	require.NotNil(t, ratio)
	assert.Equal(t, 30, ratio.Events)
	assert.Contains(t, pooledText(ratio), "30 events")
}

// The events of the pool are extracted and shown but do not rate imprecision:
// only the width of a ratio's interval does, whatever the count.
func TestPooledEventsDoNotRateImprecision(t *testing.T) {
	p, _ := poolPipeline(t)

	for _, tc := range []struct {
		name   string
		effect *model.Effect
		events int
		levels int
	}{
		{name: "299 events, not wide", effect: withEvents(reported("HR", 0.6, 0.45, 0.8), 299), events: 299},
		{name: "300 events, not wide", effect: withEvents(reported("HR", 0.6, 0.45, 0.8), 300), events: 300},
		{name: "events unknown", effect: reported("HR", 0.6, 0.45, 0.8)},
		{name: "a wide interval", effect: withEvents(reported("HR", 0.5, 0.26, 0.95), 5000), events: 5000, levels: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pe := p.poolEffects(poolState(tc.effect))
			require.NotNil(t, pe)
			assert.Equal(t, tc.events, pe.Events)

			imp := p.assessImprecision(pe)
			require.NotNil(t, imp)
			assert.Equal(t, tc.levels, imp.Levels, imp.Reason)
		})
	}
}

// blindedStudy is a randomized trial with the critic's blinding and the
// objectivity of the outcome (nil: not stated).
func blindedStudy(doi string, b model.Blinding, objective *bool, ef *model.Effect) poolStudy {
	ef.OutcomeObjective = objective

	return poolStudy{doi: doi, design: model.DesignRCT, effect: ef, blinding: b}
}

func TestPoolBlindingBias(t *testing.T) {
	yes, no := new(true), new(false)
	// a large trial (about 0.95 of the weight) and a small one
	big := func() *model.Effect { return counts(100, 1000, 200, 1000) }
	small := func() *model.Effect { return counts(5, 50, 9, 50) }

	for _, tc := range []struct {
		name    string
		studies []poolStudy
		want    bool
	}{
		{
			name: "open-label majority and a subjective outcome",
			studies: []poolStudy{
				blindedStudy(doiW1, model.BlindingOpen, no, big()),
				blindedStudy(doiW2, model.BlindingDouble, no, small()),
			},
			want: true,
		},
		{
			name: "an objective outcome",
			studies: []poolStudy{
				blindedStudy(doiW1, model.BlindingOpen, yes, big()),
				blindedStudy(doiW2, model.BlindingDouble, yes, small()),
			},
		},
		{
			name: "objectivity unknown counts as not subjective",
			studies: []poolStudy{
				blindedStudy(doiW1, model.BlindingOpen, nil, big()),
				blindedStudy(doiW2, model.BlindingOpen, nil, small()),
			},
		},
		{
			name: "unclear blinding counts as not open",
			studies: []poolStudy{
				blindedStudy(doiW1, model.BlindingUnclear, no, big()),
				blindedStudy(doiW2, model.BlindingOpen, no, small()),
			},
		},
		{
			name: "blinding missing counts as not open",
			studies: []poolStudy{
				blindedStudy(doiW1, "", no, big()),
				blindedStudy(doiW2, "", no, small()),
			},
		},
		{
			name: "open-label studies carry the minority of the weight",
			studies: []poolStudy{
				blindedStudy(doiW1, model.BlindingSingle, no, big()),
				blindedStudy(doiW2, model.BlindingOpen, no, small()),
			},
		},
		{
			name: "the subjective studies carry the minority of the weight",
			studies: []poolStudy{
				blindedStudy(doiW1, model.BlindingOpen, yes, big()),
				blindedStudy(doiW2, model.BlindingOpen, no, small()),
			},
		},
		{
			name: "a single open-label study with a subjective outcome",
			studies: []poolStudy{
				blindedStudy(doiW1, model.BlindingOpen, no, big()),
			},
			want: true,
		},
		{
			name: "mixed objectivity: the subjective studies carry most of the weight",
			studies: []poolStudy{
				blindedStudy(doiW1, model.BlindingOpen, no, big()),
				blindedStudy(doiW2, model.BlindingOpen, yes, small()),
			},
			want: true,
		},
		{
			name: "the open studies are objective and the subjective ones blinded",
			studies: []poolStudy{
				blindedStudy(doiW1, model.BlindingOpen, yes, big()),
				blindedStudy(doiW2, model.BlindingDouble, no, small()),
			},
		},
		{
			name: "blinding of a non-randomized study is not counted",
			studies: []poolStudy{
				{
					doi: doiW1, design: model.DesignCohort, blinding: model.BlindingOpen,
					effect: withObjective(adjustedEffect(reported("HR", 0.8, 0.7, 0.92)), false),
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			res := p.pool(poolCorpus(tc.studies...))
			require.NotNil(t, res.est)

			if !tc.want {
				assert.Nil(t, res.blinding)
				return
			}

			require.NotNil(t, res.blinding)
			assert.Equal(t, 1, res.blinding.Levels)
			assert.Regexp(t, `^open-label studies with a subjective or self-reported outcome carry \d+% `+
				`of the pool weight$`, res.blinding.Reason)
		})
	}
}

// The shares in the reason are the weights the pool computed: a large trial
// carries most of the pool, so the share is read from it, not counted in studies.
func TestBlindingBiasUsesThePoolWeights(t *testing.T) {
	p, _ := poolPipeline(t)

	res := p.pool(poolCorpus(
		blindedStudy(doiW1, model.BlindingOpen, new(false), counts(100, 1000, 200, 1000)),
		blindedStudy(doiW2, model.BlindingDouble, new(false), counts(5, 50, 9, 50)),
	))

	require.NotNil(t, res.blinding)
	assert.Equal(t, "open-label studies with a subjective or self-reported outcome carry 95% "+
		"of the pool weight", res.blinding.Reason)
}

// The rule is a function of the weights: shares of exactly one half do not fire.
func TestBlindingBiasNeedsMoreThanHalf(t *testing.T) {
	no := new(false)
	items := []pooledStudy{
		{blinding: model.BlindingOpen, objective: no},
		{blinding: model.BlindingDouble, objective: no},
	}

	assert.Nil(t, blindingBias(items, []float64{0.5, 0.5}))
	assert.NotNil(t, blindingBias(items, []float64{0.51, 0.49}))
	assert.Nil(t, blindingBias(nil, nil))
}

// Weights that are not the studies' own cannot come from the arithmetic: the
// pool goes on without a rating and the bug is logged at Error.
func TestRatedBlindingLogsMismatchedWeights(t *testing.T) {
	p, logs := poolPipeline(t)
	items := []pooledStudy{
		{blinding: model.BlindingOpen, objective: new(false)},
		{blinding: model.BlindingOpen, objective: new(false)},
	}

	assert.Nil(t, p.ratedBlinding(items, []float64{1}))
	assert.Contains(t, logs.String(), "level=ERROR")
	assert.Contains(t, logs.String(), "pool weights do not match the studies")
	assert.Contains(t, logs.String(), "studies=2 weights=1")

	logs.Reset()

	assert.NotNil(t, p.ratedBlinding(items, []float64{0.6, 0.4}))
	assert.Empty(t, logs.String())
}

// A corpus without a pooled estimate has no rating.
func TestBlindingBiasNeedsAPooledEstimate(t *testing.T) {
	p, _ := poolPipeline(t)

	res := p.pool(poolCorpus(poolStudy{
		doi: doiW3, design: model.DesignRCT, blinding: model.BlindingOpen, effect: withObjective(&model.Effect{}, false),
	}))

	assert.Nil(t, res.est)
	assert.Nil(t, res.blinding)
}

// The synthesis input carries the rating next to the imprecision, only when
// the rule fired; the prompt tells the judge how to read it.
func TestSynthesisInputCarriesTheBlindingBias(t *testing.T) {
	st := poolState(counts(100, 1000, 200, 1000))

	without, err := synthesisUserPrompt(st)
	require.NoError(t, err)
	assert.NotContains(t, without, "blinding_bias")

	st.BlindingBias = &model.BlindingBias{Levels: 1, Reason: "open-label studies carry 94% of the pool weight"}

	with, err := synthesisUserPrompt(st)
	require.NoError(t, err)
	assert.Contains(t, with, `"blinding_bias":{"levels":1,"reason":"open-label studies carry 94% of the pool weight"}`)

	assert.Contains(t, synthesisSystemPrompt, "When the input has blinding_bias")
	assert.Contains(t, synthesisSystemPrompt, "do not lower again for blinding")
	assert.Contains(t, synthesisSystemPrompt, "very serious (2 levels)")
}

// A critique and a state written before the new fields load, pool and reach
// the synthesis as they did: nothing about blinding appears, and events come
// only from the arms' counts (a study with only an estimate has none).
func TestOldArtifactsWithoutTheNewFields(t *testing.T) {
	const raw = `{"question":"q?","papers":[],"enriched":[
		{"doi":"10.1/w1","title":"a","flags":{}},{"doi":"10.1/w2","title":"b","flags":{}}],
		"critiques":[
		{"doi":"10.1/w1","relevance":"direct","study_design":"rct","effect":{"outcome":"pain",
			"intervention":{"events":10,"total":100},"control":{"events":20,"total":100}}},
		{"doi":"10.1/w2","relevance":"direct","study_design":"rct","effect":{"outcome":"pain",
			"measure":"RR","value":0.6,"ci_low":0.4,"ci_high":0.9}}]}`

	var st State
	require.NoError(t, json.Unmarshal([]byte(raw), &st))

	for _, c := range st.Critiques {
		assert.Empty(t, c.Blinding)
		assert.Nil(t, c.Effect.OutcomeObjective)
		assert.Nil(t, c.Effect.EventsTotal)
	}

	p, _ := poolPipeline(t)
	res := p.pool(&st)

	require.NotNil(t, res.est)
	assert.Nil(t, res.blinding)
	assert.Zero(t, res.est.Events, "the second study has no count: unknown")

	prompt, err := synthesisUserPrompt(&st)
	require.NoError(t, err)
	assert.NotContains(t, prompt, "blinding")

	// the first study alone: its arms give the events
	one := poolState(counts(10, 100, 20, 100))
	assert.Equal(t, 30, p.poolEffects(one).Events)
}

// The whole run: the critic's new fields flow into the pool, the synthesis
// input, the report and the artifact.
func TestRunRatesBlindingBias(t *testing.T) {
	const batch = `{"critiques":[` +
		`{"doi":"10.1000/a","design_quality":"high","relevance":"direct","result_direction":"null",` +
		`"study_design":"rct","blinding":"open","effect":{"outcome":"pain","outcome_objective":false,` +
		`"intervention":{"events":100,"total":1000},"control":{"events":200,"total":1000}}},` +
		`{"doi":"10.1000/b","design_quality":"high","relevance":"direct","result_direction":"null",` +
		`"study_design":"rct","blinding":"Open","effect":{"outcome":"pain","outcome_objective":false,` +
		`"intervention":{"events":120,"total":1100},"control":{"events":220,"total":1080}}}]}`

	p, _, llm := runFakes(t, batch, claimReply(claimLower))

	st, dir, err := p.Run(t.Context(), Options{
		DOIFile: doiList(t, listDOIA, listDOIB), RunDir: filepath.Join(t.TempDir(), "run"), TextMode: TextLean,
	})
	require.NoError(t, err)

	require.NotNil(t, st.BlindingBias)
	assert.Equal(t, 1, st.BlindingBias.Levels)
	assert.Contains(t, llm.prompts[1], `"blinding_bias":{"levels":1,`)
	assert.Contains(t, llm.prompts[1], `"events":640`)

	require.NotNil(t, st.Report)
	assert.Equal(t, st.BlindingBias, st.Report.BlindingBias)
	assert.Contains(t, RenderMarkdown(st),
		"**Risk of bias from lack of blinding (computed):** serious, rated down 1 level")

	loaded, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, st.BlindingBias, loaded.BlindingBias)
	assert.Equal(t, st.BlindingBias, loaded.Report.BlindingBias)
}
