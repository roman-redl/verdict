package pipeline

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	doiReflux12m = "10.1136/bmj.a2664"
	doiReflux5y  = "10.1136/bmj.f1908"
	regReflux    = "ISRCTN15517081"

	// placeholder studies and registration numbers of the trial tests
	doiOld     = "10.1/old"
	doiNew     = "10.1/new"
	doiX       = "10.1/x"
	doiY       = "10.1/y"
	doiOther   = "10.1/other"
	regNCT     = "NCT01234567"
	regNCT1    = "NCT00000001"
	regNCT2    = "NCT00000002"
	regISRCTN1 = "ISRCTN11111111"
)

// trialStudy is one direct study for the trial tests: its report year, its
// registration numbers and the effect its critic extracted.
type trialStudy struct {
	doi    string
	year   int
	ids    []string
	effect *model.Effect
	size   int // the study's own size, used when the effect has no arm counts
	design model.StudyDesign
}

// trialState is a corpus of such studies, in the order given.
func trialState(studies ...trialStudy) *State {
	st := &State{}

	for _, s := range studies {
		e := ep(s.doi)
		e.Year, e.TrialRegistrations, e.SampleSize = s.year, s.ids, s.size
		e.Abstract = "An abstract with no registration number."

		c := crit(s.doi, qualityHigh, relevanceDirect, directionPositive)
		c.Effect, c.StudyDesign = s.effect, s.design

		st.Enriched = append(st.Enriched, e)
		st.Critiques = append(st.Critiques, c)
	}

	return st
}

// The case that started it: the 12-month and the 5-year report of the REFLUX
// trial were pooled as two studies, doubling the participants. One report of
// the trial is pooled — the later one — and the other is left out with the
// reason.
func TestPoolEffectsKeepsOneReportPerTrial(t *testing.T) {
	p, logs := poolPipeline(t)

	pe := p.poolEffects(trialState(
		trialStudy{doi: doiReflux12m, year: 2008, ids: []string{regReflux}, effect: counts(59, 154, 147, 164)},
		trialStudy{doi: doiReflux5y, year: 2013, ids: []string{regReflux}, effect: counts(50, 140, 110, 150)},
	))

	require.NotNil(t, pe)
	assert.Equal(t, model.PoolSingleStudy, pe.Method)
	assert.Equal(t, 1, pe.K)
	assert.Equal(t, []string{doiReflux5y}, pe.Studies)
	assert.Equal(t, 290, pe.Participants, "the participants of the kept report only")
	assert.Equal(t, []string{doiReflux12m + " (same trial as " + doiReflux5y + ", " + regReflux + ")"}, pe.LeftOut)
	assert.Contains(t, logs.String(), "repeat report of a trial")
}

func TestPoolEffectsTrialIdentity(t *testing.T) {
	for _, tc := range []struct {
		name         string
		studies      []trialStudy
		wantStudies  []string
		wantLeftOut  []string
		wantParticip int
	}{
		{
			name: "different registration numbers stay separate",
			studies: []trialStudy{
				{doi: doiOld, year: 2008, ids: []string{regISRCTN1}, effect: counts(10, 100, 20, 100)},
				{doi: doiNew, year: 2013, ids: []string{"ISRCTN22222222"}, effect: counts(11, 100, 21, 100)},
			},
			wantStudies: []string{doiOld, doiNew}, wantParticip: 400,
		},
		{
			name: "papers without registration numbers are never merged",
			studies: []trialStudy{
				{doi: doiOld, year: 2008, effect: counts(10, 100, 20, 100)},
				{doi: doiNew, year: 2013, effect: counts(11, 100, 21, 100)},
			},
			wantStudies: []string{doiOld, doiNew}, wantParticip: 400,
		},
		{
			name: "the later year wins whatever the corpus order",
			studies: []trialStudy{
				{doi: doiNew, year: 2013, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
				{doi: doiOld, year: 2008, ids: []string{regReflux}, effect: counts(10, 300, 20, 300)},
			},
			wantStudies: []string{doiNew}, wantParticip: 200,
			wantLeftOut: []string{doiOld + " (same trial as " + doiNew + ", " + regReflux + ")"},
		},
		{
			name: "the same year: the lower DOI wins, not the larger report",
			studies: []trialStudy{
				{doi: doiOld, year: 2013, ids: []string{regReflux}, effect: counts(10, 300, 20, 300)},
				{doi: doiNew, year: 2013, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
			},
			wantStudies: []string{doiNew}, wantParticip: 200,
			wantLeftOut: []string{doiOld + " (same trial as " + doiNew + ", " + regReflux + ")"},
		},
		{
			name: "the same year and size: the lower DOI wins",
			studies: []trialStudy{
				{doi: doiNew, year: 2013, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
				{doi: doiOld, year: 2013, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
			},
			wantStudies: []string{doiNew}, wantParticip: 200,
			wantLeftOut: []string{doiOld + " (same trial as " + doiNew + ", " + regReflux + ")"},
		},
		{
			name: "an unknown year loses to a known one",
			studies: []trialStudy{
				{doi: doiOld, ids: []string{regReflux}, effect: counts(10, 300, 20, 300)},
				{doi: doiNew, year: 2013, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
			},
			wantStudies: []string{doiNew}, wantParticip: 200,
			wantLeftOut: []string{doiOld + " (same trial as " + doiNew + ", " + regReflux + ")"},
		},
		{
			name: "a chain of reports through different numbers is one trial",
			studies: []trialStudy{
				{doi: doiOld, year: 2008, ids: []string{regNCT1}, effect: counts(10, 100, 20, 100)},
				{doi: doiX, year: 2010, ids: []string{regNCT1, regISRCTN1}, effect: counts(10, 100, 20, 100)},
				{doi: doiNew, year: 2013, ids: []string{regISRCTN1}, effect: counts(10, 100, 20, 100)},
			},
			wantStudies: []string{doiNew}, wantParticip: 200,
			wantLeftOut: []string{
				doiOld + " (same trial as " + doiNew + ", NCT00000001)", // linked to the kept one through the middle report
				doiX + " (same trial as " + doiNew + ", ISRCTN11111111)",
			},
		},
		{
			name: "another trial pooled with the kept report",
			studies: []trialStudy{
				{doi: doiOld, year: 2008, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
				{doi: doiNew, year: 2013, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
				{doi: doiX, year: 2010, ids: []string{regNCT2}, effect: counts(11, 100, 21, 100)},
			},
			wantStudies: []string{doiNew, doiX}, wantParticip: 400,
			wantLeftOut: []string{doiOld + " (same trial as " + doiNew + ", " + regReflux + ")"},
		},
		{
			name: "a review that names the trial is not pooled and does not merge anything",
			studies: []trialStudy{
				{doi: doiOld, year: 2008, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
				{
					doi: doiY, year: 2020, ids: []string{regReflux}, effect: counts(10, 1000, 20, 1000),
					design: model.DesignMetaAnalysis,
				},
			},
			wantStudies: []string{doiOld}, wantParticip: 200,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			pe := p.poolEffects(trialState(tc.studies...))

			require.NotNil(t, pe)
			assert.Equal(t, tc.wantStudies, pe.Studies)
			assert.Equal(t, len(tc.wantStudies), pe.K)
			assert.Equal(t, tc.wantParticip, pe.Participants)
			assert.Equal(t, tc.wantLeftOut, pe.LeftOut)
		})
	}
}

// A second report must not tip the choice of the measure: the report of a trial
// is chosen first, and the pool with the most participants wins counted after
// the repeat reports are dropped.
func TestPoolEffectsDropsRepeatReportsBeforeChoosingTheMeasure(t *testing.T) {
	const doiHR = "10.1/hr-trial"

	p, _ := poolPipeline(t)

	pe := p.poolEffects(trialState(
		// two reports of one trial of 200 participants each: 400 as RR with the repeat, 200 without it
		trialStudy{doi: doiReflux12m, year: 2008, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
		trialStudy{doi: doiReflux5y, year: 2013, ids: []string{regReflux}, effect: counts(12, 100, 22, 100)},
		trialStudy{doi: doiHR, year: 2015, size: 300, effect: reported("HR", 0.8, 0.6, 1.1)},
	))

	require.NotNil(t, pe)
	assert.Equal(t, model.MeasureHR, pe.Measure, "300 participants beat the 200 of the one trial")
	assert.Equal(t, []string{doiHR}, pe.Studies)
	assert.Equal(t, []string{
		doiReflux12m + " (same trial as " + doiReflux5y + ", " + regReflux + ")",
		doiReflux5y + " (RR)",
	}, pe.LeftOut, "the repeat report is named as such, the kept one is left out for its measure")
}

// The repeat is dropped from the group the pool uses; a count study that is in
// both the RR and the OR candidate is dropped once, not twice.
func TestPoolEffectsRepeatReportOfACountStudyIsListedOnce(t *testing.T) {
	p, _ := poolPipeline(t)

	pe := p.poolEffects(trialState(
		trialStudy{doi: doiReflux12m, year: 2008, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
		trialStudy{doi: doiReflux5y, year: 2013, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
		trialStudy{doi: doiOther, year: 2010, effect: reported("OR", 0.5, 0.3, 0.9)},
	))

	require.NotNil(t, pe)
	assert.Equal(t, model.MeasureOR, pe.Measure)
	assert.Equal(t, []string{doiReflux5y, doiOther}, pe.Studies)
	assert.Equal(t, []string{doiReflux12m + " (same trial as " + doiReflux5y + ", " + regReflux + ")"}, pe.LeftOut)
}

// An artifact written before the registration numbers were stored still
// loads, and its abstracts name the numbers: the repeat is found all the same.
func TestPoolEffectsOldArtifactWithoutTheFieldLoads(t *testing.T) {
	const effect = `"effect":{"intervention":{"events":10,"total":100},"control":{"events":20,"total":100}}`

	raw := `{"question":"q?","papers":[],"enriched":[
		{"doi":"10.1/old","title":"12 months","year":2008,"abstract":"Trial registration: ISRCTN15517081.","flags":{}},
		{"doi":"10.1/new","title":"5 years","year":2013,"abstract":"Registered as ISRCTN 15517081.","flags":{}}],
		"critiques":[
		{"doi":"10.1/old","relevance":"direct",` + effect + `},
		{"doi":"10.1/new","relevance":"direct",` + effect + `}]}`

	var st State
	require.NoError(t, json.Unmarshal([]byte(raw), &st))
	require.Empty(t, st.Enriched[0].TrialRegistrations)

	p, _ := poolPipeline(t)
	pe := p.poolEffects(&st)

	require.NotNil(t, pe)
	assert.Equal(t, []string{doiNew}, pe.Studies)
	assert.Equal(t, []string{doiOld + " (same trial as " + doiNew + ", " + regReflux + ")"}, pe.LeftOut)
}

func TestPoolStudyRankIsDeterministic(t *testing.T) {
	// the same corpus in every order gives the same kept report
	studies := []trialStudy{
		{doi: doiA, year: 2013, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
		{doi: doiB, year: 2013, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
		{doi: doiC, year: 2013, ids: []string{regReflux}, effect: counts(10, 100, 20, 100)},
	}

	for _, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}, {1, 0, 2}} {
		p, _ := poolPipeline(t)

		var in []trialStudy
		for _, i := range order {
			in = append(in, studies[i])
		}

		pe := p.poolEffects(trialState(in...))

		require.NotNil(t, pe)
		assert.Equal(t, []string{doiA}, pe.Studies, "order %v", order)
		assert.Len(t, pe.LeftOut, 2)
	}
}
