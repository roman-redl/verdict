package pipeline

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// A trial's reports are chosen by what they measure and when: the report at the
// question's time point beats a later one that is not (REFLUX: the question asks
// for 12 months, the 5-year report is a later report of the same trial).
func TestPoolReportChoice(t *testing.T) {
	const (
		yes, no, unclear = model.TimepointYes, model.TimepointNo, model.TimepointUnclear
		same, related    = model.OutcomeSame, model.OutcomeRelated
	)

	// report is one report of the trial; its counts differ so that no two are duplicates
	report := func(
		doi string, year int, n float64, o model.OutcomeMatch, tp model.TimepointMatch, weeks float64,
	) poolStudy {
		return poolStudy{
			doi: doi, year: year, ids: []string{regReflux}, design: model.DesignRCT,
			effect: matched(counts(n, 150, n+50, 150), o, tp, weeks),
		}
	}

	for _, tc := range []struct {
		name     string
		reports  []poolStudy
		wantDOI  string
		wantLeft []string
	}{
		{
			name: "the 12-month report matches the question, the 5-year one does not",
			reports: []poolStudy{
				report(doiReflux12m, 2008, 59, same, yes, 52), report(doiReflux5y, 2013, 50, same, no, 260),
			},
			wantDOI:  doiReflux12m,
			wantLeft: []string{doiReflux5y + " (other time point)"},
		},
		{
			name: "the question asks for 5 years: the other way round",
			reports: []poolStudy{
				report(doiReflux12m, 2008, 59, same, no, 52), report(doiReflux5y, 2013, 50, same, yes, 260),
			},
			wantDOI:  doiReflux5y,
			wantLeft: []string{doiReflux12m + " (other time point)"},
		},
		{
			name: "both match: the longer follow-up",
			reports: []poolStudy{
				report(doiReflux5y, 2013, 50, same, yes, 260), report(doiReflux12m, 2008, 59, same, yes, 52),
			},
			wantDOI:  doiReflux5y,
			wantLeft: []string{doiReflux12m + " (same trial as " + doiReflux5y + ", " + regReflux + ")"},
		},
		{
			name: "a match beats an unclear time point, whatever the year and follow-up",
			reports: []poolStudy{
				report(doiReflux12m, 2008, 59, same, yes, 52), report(doiReflux5y, 2013, 50, same, unclear, 260),
			},
			wantDOI:  doiReflux12m,
			wantLeft: []string{doiReflux5y + " (same trial as " + doiReflux12m + ", " + regReflux + ")"},
		},
		{
			name: "a related outcome is not eligible: the report that matches is kept",
			reports: []poolStudy{
				report(doiReflux12m, 2008, 59, same, yes, 52), report(doiReflux5y, 2013, 50, related, yes, 260),
			},
			wantDOI:  doiReflux12m,
			wantLeft: []string{doiReflux5y + " (other outcome)"},
		},
		{
			name: "nothing is labelled (an old artifact): the later year, as before",
			reports: []poolStudy{
				report(doiReflux12m, 2008, 59, "", "", 0), report(doiReflux5y, 2013, 50, "", "", 0),
			},
			wantDOI:  doiReflux5y,
			wantLeft: []string{doiReflux12m + " (same trial as " + doiReflux5y + ", " + regReflux + ")"},
		},
		{
			name: "unclear time points: the longer follow-up, then the year",
			reports: []poolStudy{
				report(doiReflux12m, 2013, 59, same, unclear, 52), report(doiReflux5y, 2008, 50, same, unclear, 260),
			},
			wantDOI:  doiReflux5y,
			wantLeft: []string{doiReflux12m + " (same trial as " + doiReflux5y + ", " + regReflux + ")"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := poolPipeline(t)

			pe := p.poolEffects(poolCorpus(tc.reports...))

			require.NotNil(t, pe)
			assert.Equal(t, []string{tc.wantDOI}, pe.Studies)
			assert.Equal(t, tc.wantLeft, pe.LeftOut)
		})
	}
}

// The report is chosen before the measure: the kept report of a trial that has
// only an HR is the one at the question's time point, though another report has
// counts and more participants.
func TestPoolReportChosenBeforeTheMeasure(t *testing.T) {
	p, _ := poolPipeline(t)

	pe := p.poolEffects(poolCorpus(
		poolStudy{
			doi: doiReflux12m, year: 2008, ids: []string{regReflux}, design: model.DesignRCT, size: 100,
			effect: matched(reported("HR", 0.7, 0.5, 0.98), model.OutcomeSame, model.TimepointYes, 52),
		},
		poolStudy{
			doi: doiReflux5y, year: 2013, ids: []string{regReflux}, design: model.DesignRCT,
			effect: matched(counts(50, 400, 110, 400), model.OutcomeSame, model.TimepointUnclear, 260),
		},
	))

	require.NotNil(t, pe)
	assert.Equal(t, model.MeasureHR, pe.Measure)
	assert.Equal(t, []string{doiReflux12m}, pe.Studies)
}

// Two papers with exactly the same arm data report the same results: one is
// pooled (the first published), the other is named. A correction notice that
// repeats the abstract of the paper it corrects is the q193 case.
func TestPoolExactDuplicates(t *testing.T) {
	const (
		support = "10.1056/nejmoa0911"
		notice  = "10.1056/nejmx100030"
	)

	meansEffect := means(2, unitPoints)

	for _, tc := range []struct {
		name        string
		studies     []poolStudy
		wantStudies []string
		wantLeft    []string
	}{
		{
			name: "a correction notice with the abstract's counts",
			studies: []poolStudy{
				{doi: notice, year: 2010, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: support, year: 2010, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialA, year: 2012, design: model.DesignRCT, effect: counts(15, 120, 30, 118)},
			},
			wantStudies: []string{support, trialA}, // the same year: the lower DOI is kept
			wantLeft:    []string{notice + " (same results as " + support + ")"},
		},
		{
			name: "the earlier publication is kept, whatever the order",
			studies: []poolStudy{
				{doi: trialA, year: 2012, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialB, year: 2009, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
			},
			wantStudies: []string{trialB},
			wantLeft:    []string{trialA + " (same results as " + trialB + ")"},
		},
		{
			name: "identical means, SDs and sizes",
			studies: []poolStudy{
				{doi: trialA, year: 2012, design: model.DesignRCT, effect: meansEffect},
				{doi: trialB, year: 2013, design: model.DesignRCT, effect: means(2, unitPoints)},
				{doi: trialC, year: 2014, design: model.DesignRCT, effect: shifted(meansEffect)},
			},
			wantStudies: []string{trialA, trialC},
			wantLeft:    []string{trialB + " (same results as " + trialA + ")"},
		},
		{
			name: "one event more is another study",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialB, design: model.DesignRCT, effect: counts(10, 100, 21, 100)},
			},
			wantStudies: []string{trialA, trialB},
		},
		{
			name: "the arms swapped are not the same numbers",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialB, design: model.DesignRCT, effect: counts(20, 100, 10, 100)},
			},
			wantStudies: []string{trialA, trialB},
		},
		{
			name: "reported estimates carry no arm data to compare",
			studies: []poolStudy{
				{doi: trialA, design: model.DesignRCT, effect: reported("HR", 0.7, 0.5, 0.98)},
				{doi: trialB, design: model.DesignRCT, effect: reported("HR", 0.7, 0.5, 0.98)},
			},
			wantStudies: []string{trialA, trialB},
		},
		{
			name: "three copies leave the first published",
			studies: []poolStudy{
				{doi: trialC, year: 2013, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialA, year: 2011, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: trialB, year: 2012, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				{doi: cohortA, year: 2012, design: model.DesignRCT, effect: counts(9, 100, 20, 100)},
			},
			wantStudies: []string{trialA, cohortA},
			wantLeft: []string{
				trialC + " (same results as " + trialA + ")", trialB + " (same results as " + trialA + ")",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, logs := poolPipeline(t)

			pe := p.poolEffects(poolCorpus(tc.studies...))

			require.NotNil(t, pe)
			assert.ElementsMatch(t, tc.wantStudies, pe.Studies)
			assert.Equal(t, tc.wantLeft, pe.LeftOut)

			if len(tc.wantLeft) > 0 {
				assert.Contains(t, logs.String(), `"effects not pooled" reason="same results"`)
			}
		})
	}
}

// A paper whose publication types say it is not a study report is never pooled,
// and does not decide the stratum.
func TestPoolSkipsPapersThatAreNotStudyReports(t *testing.T) {
	for _, types := range [][]string{
		{pubErratum},
		{pubComment},
		{"editorial"},
		{pubLetter},
		{"news"},
		{pubRetract},
		{"expression of concern"},
		{pubArticle, oaErratum},
	} {
		t.Run(types[len(types)-1], func(t *testing.T) {
			p, logs := poolPipeline(t)

			pe := p.poolEffects(poolCorpus(
				poolStudy{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
				poolStudy{doi: trialB, design: model.DesignRCT, effect: counts(11, 100, 21, 100), pubTypes: types},
			))

			require.NotNil(t, pe)
			assert.Equal(t, []string{trialA}, pe.Studies)
			assert.Empty(t, pe.LeftOut, "not a study: it is not a study left out either")
			assert.Contains(t, logs.String(), "not a study report")
		})
	}

	t.Run("a research letter typed as a trial is a study", func(t *testing.T) {
		p, _ := poolPipeline(t)

		pe := p.poolEffects(poolCorpus(
			poolStudy{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)},
			poolStudy{
				doi: trialB, design: model.DesignRCT, effect: counts(11, 100, 21, 100),
				pubTypes: []string{pubLetter, pubTrial},
			},
		))

		require.NotNil(t, pe)
		assert.Equal(t, []string{trialA, trialB}, pe.Studies)
	})
}

// The whole run on the q159 shape: a randomized trial without numbers and a
// cohort with an adjusted hazard ratio. Nothing is pooled, the report says why,
// and the judge gets no pooled estimate to lean on.
func TestRunTrialWithoutNumbersAndCohortLeavesNoPool(t *testing.T) {
	reply := `{"critiques":[` +
		`{"doi":"10.1000/a","design_quality":"high","relevance":"direct","result_direction":"null","study_design":"rct"},` +
		`{"doi":"10.1000/b","design_quality":"moderate","relevance":"direct","result_direction":"positive",` +
		`"study_design":"cohort","effect":{"outcome":"mortality","outcome_match":"same","timepoint_match":"yes",` +
		`"adjusted":true,"measure":"HR","value":0.55,"ci_low":0.4,"ci_high":0.75}}]}`

	p, _, llm := runFakes(t, reply, effectSynthesisReply)

	st, _, err := p.Run(t.Context(), Options{
		DOIFile: doiList(t, listDOIA, listDOIB), RunDir: filepath.Join(t.TempDir(), "run"), TextMode: TextLean,
	})
	require.NoError(t, err)

	assert.Nil(t, st.Pooled)
	require.Len(t, llm.prompts, 2)
	assert.NotContains(t, llm.prompts[1], `"pooled_estimate"`)
	assert.Contains(t, llm.prompts[1], `"adjusted":true`, "the judge still reads the cohort's own effect")

	require.NotNil(t, st.Report)
	assert.Contains(t, st.Report.Warnings,
		"no pooled estimate; studies with numbers that were not pooled: 1 non-randomized")
	assert.Equal(t, "1 positive / 1 null", directionTally(st), "both primary studies are counted")
}

// The synthesis learns which stratum the numbers are of, so that the starting
// level follows it.
func TestSynthesisInputCarriesTheStratum(t *testing.T) {
	p, _ := poolPipeline(t)

	st := poolCorpus(poolStudy{doi: trialA, design: model.DesignRCT, effect: counts(10, 100, 20, 100)})
	st.Pooled = p.poolEffects(st)

	prompt, err := synthesisUserPrompt(st)
	require.NoError(t, err)
	assert.Contains(t, prompt, `"stratum":"randomized"`)

	assert.Contains(t, synthesisSystemPrompt, `"randomized" starts at strong, "non-randomized" at weak`)
	assert.Contains(t, synthesisSystemPrompt, "best design among the direct studies")
	assert.Contains(t, synthesisSystemPrompt, "its reason in parentheses")
}

// A pooled estimate written before the stratum existed loads, and is shown
// without one.
func TestPooledEstimateWithoutStratumLoads(t *testing.T) {
	var pe model.PooledEstimate

	old := `{"method":"single_study","measure":"RR","estimate":0.5,"ci_low":0.3,"ci_high":0.8,"k":1,"studies":["10.1/a"]}`
	require.NoError(t, json.Unmarshal([]byte(old), &pe))

	assert.Empty(t, pe.Stratum)
	assert.NotContains(t, pooledText(&pe), "design:")
}
