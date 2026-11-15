package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// meansOf is a continuous outcome: the means of the two arms with an SD of 5
// and one size, in points.
func meansOf(intervention, control, n float64) *model.Effect {
	const sd = 5.0

	return &model.Effect{
		Unit:         unitPoints,
		Intervention: &model.Arm{Mean: new(intervention), SD: new(sd), N: new(n)},
		Control:      &model.Arm{Mean: new(control), SD: new(sd), N: new(n)},
	}
}

// robustnessOf pools the corpus and runs the check the way the synthesis does.
func robustnessOf(t *testing.T, p *Pipeline, st *State, claim string) *model.Robustness {
	t.Helper()

	res := p.pool(st)
	require.NotNil(t, res.est)

	return p.assessRobustness(res, p.assessImprecision(res.est), claim)
}

func TestAssessRobustness(t *testing.T) {
	// two small trials whose pooled interval includes no effect, and one large
	// trial that brings it to exclude it
	smallAndLarge := []*model.Effect{counts(32, 300, 40, 300), counts(48, 500, 60, 500), counts(120, 1000, 200, 1000)}

	for _, tc := range []struct {
		name    string
		effects []*model.Effect
		claim   string
		checked int
		single  bool
		// decisive is the studies without which the direction changes, with the
		// direction it changes to
		decisive map[string]model.EffectDirection
	}{
		{
			name:    "risk ratio, the direction holds without any study",
			effects: []*model.Effect{counts(10, 100, 20, 100), counts(20, 200, 40, 200), counts(15, 150, 30, 150)},
			claim:   claimLower, checked: 3,
		},
		{
			name: "odds ratio, the direction holds without any study",
			effects: []*model.Effect{
				reported("OR", 0.55, 0.40, 0.75), reported("OR", 0.60, 0.42, 0.86), reported("OR", 0.50, 0.33, 0.76),
			},
			claim: claimLower, checked: 3,
		},
		{
			name:    "risk ratio, the large trial carries the conclusion",
			effects: smallAndLarge, claim: claimLower, checked: 3,
			decisive: map[string]model.EffectDirection{study3: model.EffectNoEffect},
		},
		{
			name:    "the direction against the claim changes the same way",
			effects: smallAndLarge, claim: claimHigher, checked: 3,
			decisive: map[string]model.EffectDirection{study3: model.EffectNoEffect},
		},
		{
			name: "mean difference, the large trial carries the conclusion",
			effects: []*model.Effect{
				meansOf(10.8, 10, 100), meansOf(11.8, 11, 100), meansOf(12.5, 10, 500),
			},
			claim: claimHigher, checked: 3,
			decisive: map[string]model.EffectDirection{study3: model.EffectNoEffect},
		},
		{
			name: "standardized difference, the direction holds without any study",
			effects: []*model.Effect{
				reported("SMD", 0.5, 0.2, 0.8), reported("SMD", 0.6, 0.25, 0.95), reported("SMD", 0.45, 0.1, 0.8),
			},
			claim: claimHigher, checked: 3,
		},
		{
			name:    "two studies, each left out leaves a single study",
			effects: []*model.Effect{counts(32, 300, 40, 300), counts(120, 1000, 200, 1000)},
			claim:   claimLower, checked: 2,
			decisive: map[string]model.EffectDirection{study2: model.EffectNoEffect},
		},
		{
			name: "a single study rests on itself", effects: []*model.Effect{counts(10, 689, 18, 897)},
			claim: claimLower, checked: 1, single: true,
		},
		{
			name: "a single study needs no claim_change", effects: []*model.Effect{counts(10, 689, 18, 897)},
			checked: 1, single: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, logs := poolPipeline(t)

			got := robustnessOf(t, p, poolState(tc.effects...), tc.claim)

			require.NotNil(t, got, logs.String())
			assert.Equal(t, tc.checked, got.Checked)
			assert.Equal(t, tc.single, got.SingleStudy)
			assert.Len(t, got.Decisive, len(tc.decisive))

			for _, d := range got.Decisive {
				assert.Equal(t, tc.decisive[d.Study], d.DirectionWithout, d.Study)
				assert.Equal(t, d.Study, d.Title, "the title comes from the paper")
				assert.Positive(t, d.Weight)
			}
		})
	}
}

// The decisive study is named with its size and its share of the pool.
func TestAssessRobustnessNamesTheDecisiveStudy(t *testing.T) {
	p, _ := poolPipeline(t)

	st := poolState(counts(32, 300, 40, 300), counts(48, 500, 60, 500), counts(120, 1000, 200, 1000))
	st.Enriched[2].Title = "The large trial"
	st.Enriched[2].SampleSize = 2000

	res := p.pool(st)
	got := p.assessRobustness(res, p.assessImprecision(res.est), claimLower)

	require.NotNil(t, got)
	require.Len(t, got.Decisive, 1)

	d := got.Decisive[0]
	assert.Equal(t, study3, d.Study)
	assert.Equal(t, "The large trial", d.Title)
	assert.Equal(t, 2000, d.Participants)
	assert.InDelta(t, res.est.Weights[2], d.Weight, 1e-12)
	assert.Greater(t, d.Weight, 0.5)
}

// The size shown for a decisive study is the one the study list shows (the
// critic's count of the whole trial), not the pool's n: the compared arms,
// divided by the design effect for a cluster trial.
func TestAssessRobustnessShowsTheListSizeOfAClusterTrial(t *testing.T) {
	p, logs := poolPipeline(t)

	cluster := clusterTrial(counts(120, 1000, 200, 1000), &model.Cluster{Clusters: new(100.0), ICC: new(0.001)})
	cluster.doi, cluster.sizeReported = trialC, 5000

	res := p.pool(poolCorpus(
		poolStudy{doi: trialA, design: model.DesignRCT, effect: counts(32, 300, 40, 300)},
		poolStudy{doi: trialB, design: model.DesignRCT, effect: counts(48, 500, 60, 500)},
		cluster,
	))
	require.NotNil(t, res.est)

	got := p.assessRobustness(res, p.assessImprecision(res.est), claimLower)

	require.NotNil(t, got, logs.String())
	require.Len(t, got.Decisive, 1)

	d := got.Decisive[0]
	assert.Equal(t, trialC, d.Study)
	assert.Equal(t, 5000, d.Participants, "the list size, not the effective sample of the compared arms")
	assert.Less(t, res.group.items[2].n, 2000, "the pool's n is divided by the design effect")
}

// A whole pool that needs no claim_change (the interval includes no effect) and
// a smaller one that does: without the claim_change its direction cannot be
// stated, which is still a change, so the study is decisive rather than the
// check being dropped.
func TestAssessRobustnessSmallerPoolNeedsTheClaim(t *testing.T) {
	p, logs := poolPipeline(t)

	got := robustnessOf(t, p, poolState(counts(10, 100, 20, 100), counts(20, 200, 40, 200), counts(30, 100, 25, 100)), "")

	require.NotNil(t, got, logs.String())
	assert.Equal(t, model.EffectUnclear, got.Direction)
	require.Len(t, got.Decisive, 1)
	assert.Equal(t, study3, got.Decisive[0].Study)
	assert.Equal(t, model.EffectUnclear, got.Decisive[0].DirectionWithout)
}

// A mean-difference pool without an SD (a reported estimate has none) has no
// band of trivial effects: a smaller pool of the same data does not gain one,
// or a tiny study would look decisive for the band alone.
func TestAssessRobustnessKeepsTheBandAvailabilityOfTheWholePool(t *testing.T) {
	p, logs := poolPipeline(t)

	estimate := reported("MD", 0.3, -2.0, 2.6)
	estimate.Unit = unitPoints

	res := p.pool(poolState(meansOf(10.2, 10, 20), meansOf(11.3, 11, 20), estimate))
	require.NotNil(t, res.est)
	require.Equal(t, model.MeasureMD, res.est.Measure)
	require.Equal(t, 3, res.est.K)
	require.Nil(t, res.est.SD, "a reported estimate leaves the whole pool without an SD")

	got := p.assessRobustness(res, p.assessImprecision(res.est), claimHigher)

	require.NotNil(t, got, logs.String())
	assert.Empty(t, got.Decisive, "the two means studies alone have a band; the whole pool has none")
}

// A smaller pool that cannot be rated is logged as that, not as an unusable
// pooled estimate.
func TestDirectionWithoutLogsAnUnratableSmallerPool(t *testing.T) {
	p, logs := poolPipeline(t)

	res := p.pool(poolState(counts(10, 100, 20, 100), counts(20, 200, 40, 200), counts(15, 150, 30, 150)))
	require.NotNil(t, res.est)

	g := *res.group
	g.measure = "ZZ"

	_, _, ok := p.directionWithout(&g, res.est, 0, claimLower)

	assert.False(t, ok)
	assert.Contains(t, logs.String(), "robustness not computed: the pool without a study cannot be rated")
	assert.NotContains(t, logs.String(), "imprecision not computed")
}

func TestAssessRobustnessNothingToCheck(t *testing.T) {
	p, _ := poolPipeline(t)

	res := p.pool(poolState(nil, nil))
	assert.Nil(t, p.assessRobustness(res, nil, claimLower), "no pool: the studies are judged one by one")
	assert.Nil(t, p.assessRobustness(p.pool(&State{}), nil, claimLower))
}

// A direction that needs the claim's change cannot be derived without it: the
// check is skipped with a warning, not guessed.
func TestAssessRobustnessWithoutADirection(t *testing.T) {
	st := func() *State {
		return poolState(counts(10, 100, 20, 100), counts(20, 200, 40, 200), counts(15, 150, 30, 150))
	}

	for _, tc := range []struct {
		name  string
		claim string
		noImp bool
	}{
		{name: "no claim_change", claim: ""},
		{name: "an invalid claim_change", claim: "same"},
		{name: "no imprecision rating", claim: claimLower, noImp: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, logs := poolPipeline(t)

			res := p.pool(st())
			imp := p.assessImprecision(res.est)

			if tc.noImp {
				imp = nil
			}

			assert.Nil(t, p.assessRobustness(res, imp, tc.claim))
			assert.Contains(t, logs.String(), "robustness not computed")
		})
	}
}

// A pool whose conclusion needs no claim (the interval includes no effect) is
// checked without a claim_change.
func TestAssessRobustnessNeedsNoClaimForNoEffect(t *testing.T) {
	p, logs := poolPipeline(t)

	got := robustnessOf(t, p, poolState(counts(10, 100, 11, 100), counts(12, 120, 13, 120), counts(9, 90, 8, 90)), "")

	require.NotNil(t, got, logs.String())
	assert.Equal(t, 3, got.Checked)
	assert.Empty(t, got.Decisive)
}

// A weight list that does not match the studies is a bug, not data: logged at
// Error, no robustness claimed.
func TestAssessRobustnessLogsMismatchedWeights(t *testing.T) {
	p, logs := poolPipeline(t)

	res := p.pool(poolState(counts(10, 100, 20, 100), counts(20, 200, 40, 200)))
	res.est.Weights = res.est.Weights[:1]

	assert.Nil(t, p.assessRobustness(res, p.assessImprecision(res.est), claimLower))
	assert.Contains(t, logs.String(), "pool weights do not match")
}

// The check only reads the pool: the pooled estimate and the group it was made
// from stay as they were.
func TestAssessRobustnessLeavesThePoolAlone(t *testing.T) {
	p, _ := poolPipeline(t)

	res := p.pool(poolState(counts(32, 300, 40, 300), counts(48, 500, 60, 500), counts(120, 1000, 200, 1000)))
	before, studies := *res.est, len(res.group.items)

	require.NotNil(t, p.assessRobustness(res, p.assessImprecision(res.est), claimLower))

	assert.Equal(t, before, *res.est)
	assert.Len(t, res.group.items, studies)
	assert.Equal(t, study1, res.group.items[0].doi)
}

// The whole run: the check is stored in the state and the report, shown in
// report.md and kept out of what the judge reads.
func TestRunChecksRobustness(t *testing.T) {
	p, _, llm := runFakes(t, effectBatchReply, claimReply(claimLower))

	st, dir, err := p.Run(t.Context(), Options{
		DOIFile: doiList(t, listDOIA, listDOIB), RunDir: filepath.Join(t.TempDir(), "run"), TextMode: TextLean,
	})
	require.NoError(t, err)

	require.NotNil(t, st.Robustness)
	assert.Equal(t, 2, st.Robustness.Checked)
	assert.False(t, st.Robustness.SingleStudy)
	assert.Equal(t, st.Robustness, st.Report.Robustness)
	assert.NotContains(t, llm.prompts[1], "robustness", "the judge does not read the check")

	//nolint:gosec // reads a file the run just wrote into the test's temp directory
	report, err := os.ReadFile(filepath.Join(dir, "report.md"))
	require.NoError(t, err)
	assert.Contains(t, string(report), "**Robustness (computed):** ")

	loaded, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, st.Robustness, loaded.Robustness, "the check round-trips through the artifact")
	assert.Equal(t, st.Robustness, loaded.Report.Robustness)
}

// Without a pooled estimate there is nothing to check; neither the state nor the
// report mentions it.
func TestRunWithoutAPoolHasNoRobustness(t *testing.T) {
	noNumbers := `{"critiques":[` +
		`{"doi":"10.1000/a","design_quality":"high","relevance":"direct","result_direction":"null"},` +
		`{"doi":"10.1000/b","design_quality":"high","relevance":"direct","result_direction":"null"}]}`
	p, _, _ := runFakes(t, noNumbers, effectSynthesisReply)

	st, dir, err := p.Run(t.Context(), Options{
		DOIFile: doiList(t, listDOIA, listDOIB), RunDir: filepath.Join(t.TempDir(), "run"), TextMode: TextLean,
	})
	require.NoError(t, err)

	assert.Nil(t, st.Robustness)
	assert.Nil(t, st.Report.Robustness)

	//nolint:gosec // reads a file the run just wrote into the test's temp directory
	report, err := os.ReadFile(filepath.Join(dir, "report.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(report), "Robustness")
}

func TestRobustnessText(t *testing.T) {
	trial := func(n int, title string, weight float64, d model.EffectDirection) model.DecisiveStudy {
		return model.DecisiveStudy{Study: doiX, Title: title, Participants: n, Weight: weight, DirectionWithout: d}
	}
	four := []model.DecisiveStudy{
		trial(100, "A", 0.4, model.EffectNoEffect), trial(200, "B", 0.3, model.EffectUnclear),
		trial(300, "C", 0.2, model.EffectOpposite), trial(400, "D", 0.1, model.EffectNoEffect),
	}

	for _, tc := range []struct {
		name string
		in   *model.Robustness
		en   string
		ru   string
	}{
		{
			name: "the direction holds", in: &model.Robustness{Checked: 4},
			en: "the direction holds when any one of the 4 studies is left out",
			ru: "направление сохраняется, если исключить любое одно из 4 исследований",
		},
		{
			name: "the direction holds, 21 studies", in: &model.Robustness{Checked: 21},
			en: "the direction holds when any one of the 21 studies is left out",
			ru: "направление сохраняется, если исключить любое одно из 21 исследования",
		},
		{
			name: "no direction to hold when the whole pool's is unclear",
			in:   &model.Robustness{Checked: 4, Direction: model.EffectUnclear},
			en:   "the conclusion does not change when any one of the 4 studies is left out",
			ru:   "вывод не меняется, если исключить любое одно из 4 исследований",
		},
		{
			name: "a stated direction holds",
			in:   &model.Robustness{Checked: 4, Direction: model.EffectSupports},
			en:   "the direction holds when any one of the 4 studies is left out",
			ru:   "направление сохраняется, если исключить любое одно из 4 исследований",
		},
		{
			name: "a weight under half a percent",
			in: &model.Robustness{Checked: 2, Decisive: []model.DecisiveStudy{
				{Study: doiX, Weight: 0.004, DirectionWithout: model.EffectNoEffect},
			}},
			en: `fragile — without "10.1/x" (<1% of the weight) the direction becomes "no meaningful effect"`,
			ru: `хрупкий вывод: без исследования «10.1/x» (<1% веса) направление становится таким: «значимого эффекта нет»`,
		},
		{
			name: "half a percent rounds up",
			in: &model.Robustness{Checked: 2, Decisive: []model.DecisiveStudy{
				{Study: doiX, Weight: 0.005, DirectionWithout: model.EffectNoEffect},
			}},
			en: `fragile — without "10.1/x" (1% of the weight) the direction becomes "no meaningful effect"`,
			ru: `хрупкий вывод: без исследования «10.1/x» (1% веса) направление становится таким: «значимого эффекта нет»`,
		},
		{
			name: "a single study", in: &model.Robustness{Checked: 1, SingleStudy: true},
			en: "the pooled result rests on one study",
			ru: "объединённый результат опирается на одно исследование",
		},
		{
			name: "one decisive study",
			in: &model.Robustness{Checked: 3, Decisive: []model.DecisiveStudy{
				trial(2000, "The large trial", 0.62, model.EffectNoEffect),
			}},
			en: `fragile — without "The large trial" (2000 participants, 62% of the weight) ` +
				`the direction becomes "no meaningful effect"`,
			ru: `хрупкий вывод: без исследования «The large trial» (2000 участников, 62% веса) ` +
				`направление становится таким: «значимого эффекта нет»`,
		},
		{
			name: "the size is not stated, the title is the DOI",
			in: &model.Robustness{Checked: 2, Decisive: []model.DecisiveStudy{
				{Study: doiX, Weight: 0.5, DirectionWithout: model.EffectUnclear},
			}},
			en: `fragile — without "10.1/x" (50% of the weight) the direction becomes "cannot be stated"`,
			ru: `хрупкий вывод: без исследования «10.1/x» (50% веса) направление становится таким: «определить нельзя»`,
		},
		{
			name: "up to three are named",
			in:   &model.Robustness{Checked: 9, Decisive: four},
			en: `fragile — without "A" (100 participants, 40% of the weight) the direction becomes "no meaningful effect"; ` +
				`without "B" (200 participants, 30% of the weight) the direction becomes "cannot be stated"; ` +
				`without "C" (300 participants, 20% of the weight) the direction becomes "opposite effect", and 1 more`,
			ru: `хрупкий вывод: без исследования «A» (100 участников, 40% веса) направление становится таким: ` +
				`«значимого эффекта нет»; без исследования «B» (200 участников, 30% веса) направление становится таким: ` +
				`«определить нельзя»; без исследования «C» (300 участников, 20% веса) направление становится таким: ` +
				`«обратный эффект» и ещё 1`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.en, english().robustnessText(tc.in))
			assert.Equal(t, tc.ru, russian().robustnessText(tc.in))
		})
	}
}

// A title is cut to a short one and cannot break the line.
func TestRobustnessTextShortensTheTitle(t *testing.T) {
	long := strings.Repeat("word ", 30)
	got := english().robustnessText(&model.Robustness{Checked: 2, Decisive: []model.DecisiveStudy{
		{Study: doiX, Title: long, Weight: 0.5, DirectionWithout: model.EffectNoEffect},
	}})

	assert.Contains(t, got, `without "word word`)
	assert.Contains(t, got, `…" (50% of the weight)`)
}

// The line sits right after the imprecision line, before the blinding one.
func TestRobustnessLineFollowsImprecision(t *testing.T) {
	md := RenderMarkdown(reportState())

	imp := strings.Index(md, "**Imprecision (computed):**")
	rob := strings.Index(md, "**Robustness (computed):**")
	blind := strings.Index(md, "**Risk of bias from lack of blinding (computed):**")

	assert.Less(t, imp, rob)
	assert.Less(t, rob, blind)
}
