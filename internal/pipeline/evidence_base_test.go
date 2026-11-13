package pipeline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	doiCochrane   = "10.1002/14651858.CD012345.pub2"
	regEvidence   = "NCT00012345"
	regEvidenceB  = "NCT00054321"
	relevanceOff  = "tangential"
	typeErratumEv = "erratum"
	verdictWeakEv = "weak"
)

// evPaper is one paper of an evidence-base test: what the critic judged and
// what the enrichment knows. A zero relevance means direct.
type evPaper struct {
	doi       string
	title     string
	year      int
	relevance string
	design    model.StudyDesign
	blinding  model.Blinding
	size      int // the critic's count
	metaSize  int // the discovery metadata's count
	regs      []string
	retracted bool
	types     []string
	authors   []string
	failed    bool   // the critic's call failed: the critique carries an error
	direction string // the critic's result_direction
}

func evState(papers ...evPaper) *State {
	st := &State{}

	for i := range papers {
		p := &papers[i]

		relevance := p.relevance
		if relevance == "" {
			relevance = relevanceDirect
		}

		title := p.title
		if title == "" {
			title = "Study " + p.doi
		}

		st.Enriched = append(st.Enriched, model.EnrichedPaper{
			DOI: p.doi, Title: title, Year: p.year, SampleSize: p.metaSize,
			PublicationTypes: p.types, Authors: p.authors,
			TrialRegistrations: p.regs,
			Flags:              model.RedFlags{Retracted: p.retracted},
		})
		c := model.Critique{
			DOI: p.doi, Relevance: relevance, StudyDesign: p.design, Blinding: p.blinding,
			SampleSizeReported: p.size, ResultDirection: p.direction,
		}

		if p.failed {
			c = blankCritique(st.Enriched[i])
			c.Error = "call failed"
		}

		st.Critiques = append(st.Critiques, c)
	}

	return st
}

func TestEvidenceBaseDesignGroups(t *testing.T) {
	st := evState(
		evPaper{doi: "10.1/r1", title: "Trial one", design: model.DesignRCT, blinding: model.BlindingDouble, size: 100},
		evPaper{doi: "10.1/r2", title: "Trial two", design: model.DesignRCT, blinding: model.BlindingDouble, size: 300},
		evPaper{
			doi: "10.1/r3", title: "Trial three", design: model.DesignClusterRCT, blinding: model.BlindingOpen, size: 500,
		},
		evPaper{doi: "10.1/o1", title: "Cohort one", design: model.DesignCohort, size: 1000},
		evPaper{doi: "10.1/o2", title: "Non-randomized", design: model.DesignNonrandomizedTrial, size: 200},
		evPaper{doi: "10.1/s1", design: model.DesignSystematicReview, size: 9000},
		evPaper{doi: doiCochrane, design: model.DesignMetaAnalysis},
		evPaper{doi: "10.1/n1", design: model.DesignNarrativeReview},
		evPaper{doi: "10.1/g1", design: model.DesignGuideline},
		evPaper{doi: "10.1/u1", design: model.DesignUnclear},
		evPaper{doi: "10.1/i1", relevance: relevanceIndirect, design: model.DesignRCT, size: 50},
		evPaper{doi: "10.1/i2", relevance: relevanceOff},
		evPaper{doi: "10.1/i3", relevance: "unknown"},
		evPaper{doi: "10.1/i4", relevance: " "},
	)

	eb := evidenceBaseEN(st, nil)
	require.NotNil(t, eb)

	assert.Equal(t, 14, eb.Reviewed)
	assert.Equal(t, 10, eb.Direct)
	assert.Equal(t, 4, eb.Indirect, "indirect, tangential, unknown and a blank relevance are one number")
	assert.Equal(t, 3, eb.Other, "a narrative review, a guideline and an unclear design")
	assert.Zero(t, eb.RepeatReports)

	assert.Equal(t, &model.EvidenceGroup{
		Studies: 3, DoubleBlind: 2, Open: 1, Participants: 900, MedianSize: 300, Largest: 500, LargestStudy: "Trial three",
	}, eb.Randomized)
	assert.Equal(t, &model.EvidenceGroup{
		Studies: 2, Participants: 1200, MedianSize: 600, Largest: 1000, LargestStudy: "Cohort one",
	}, eb.Observational, "a non-randomized trial is counted with the observational studies")
	assert.Equal(t, &model.EvidenceGroup{Studies: 2, Cochrane: 1}, eb.Reviews, "a review carries no participants")

	assert.Equal(t, "14 papers reviewed; 10 answer the question directly: "+
		"3 randomized trials (2 double-blind, 1 open-label; 900 participants, median 300, largest 500 — Trial three), "+
		"2 observational studies (1200 participants, median 600, largest 1000 — Cohort one), "+
		"2 systematic reviews or meta-analyses (1 Cochrane), "+
		"3 other publications (narrative reviews, guidelines, other or unclear designs). "+
		"Not pooled: no direct study with usable numbers on the outcome. Indirect or off-topic: 4.", eb.Summary)
}

func TestEvidenceBaseBlinding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		blinding []model.Blinding
		want     model.EvidenceGroup // only the blinding counters
		text     string
	}{
		{
			name: "every value",
			blinding: []model.Blinding{
				model.BlindingDouble, model.BlindingSingle, model.BlindingOpen, model.BlindingUnclear, "",
			},
			want: model.EvidenceGroup{DoubleBlind: 1, SingleBlind: 1, Open: 1, BlindingNotStated: 1, BlindingNotAssessed: 1},
			text: "1 double-blind, 1 single-blind, 1 open-label, 1 blinding not stated, 1 blinding not assessed",
		},
		{
			name:     "unclear reads as not stated",
			blinding: []model.Blinding{model.BlindingUnclear, model.BlindingUnclear},
			want:     model.EvidenceGroup{BlindingNotStated: 2},
			text:     "2 blinding not stated",
		},
		{
			name:     "a missing value (a critique from before the field) reads as not assessed",
			blinding: []model.Blinding{"", ""},
			want:     model.EvidenceGroup{BlindingNotAssessed: 2},
			text:     "2 blinding not assessed",
		},
		{
			name:     "all open",
			blinding: []model.Blinding{model.BlindingOpen, model.BlindingOpen},
			want:     model.EvidenceGroup{Open: 2},
			text:     "2 open-label",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var papers []evPaper

			for i, b := range tc.blinding {
				papers = append(papers, evPaper{
					doi: "10.1/b" + string(rune('a'+i)), design: model.DesignRCT, blinding: b, size: 100,
				})
			}

			g := buildEvidenceBase(evState(papers...), nil).Randomized
			require.NotNil(t, g)

			got := model.EvidenceGroup{
				DoubleBlind: g.DoubleBlind, SingleBlind: g.SingleBlind, Open: g.Open,
				BlindingNotStated: g.BlindingNotStated, BlindingNotAssessed: g.BlindingNotAssessed,
			}
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.text, blindingText(g))
		})
	}
}

// The blinding of a trial reported several times: a stated value wins, then
// "unclear", and only when no report has the field is it not assessed.
func TestEvidenceBaseBlindingOfRepeatReports(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b model.Blinding
		want model.EvidenceGroup // the blinding counters
	}{
		{"stated beats unclear", model.BlindingUnclear, model.BlindingOpen, model.EvidenceGroup{Open: 1}},
		{"stated beats missing", "", model.BlindingDouble, model.EvidenceGroup{DoubleBlind: 1}},
		{"unclear beats missing", "", model.BlindingUnclear, model.EvidenceGroup{BlindingNotStated: 1}},
		{"neither has the field", "", "", model.EvidenceGroup{BlindingNotAssessed: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := buildEvidenceBase(evState(
				evPaper{doi: doiA, year: 2008, design: model.DesignRCT, blinding: tc.a, regs: []string{regEvidence}},
				evPaper{doi: doiB, year: 2013, design: model.DesignRCT, blinding: tc.b, regs: []string{regEvidence}},
			), nil).Randomized
			require.NotNil(t, g)

			assert.Equal(t, 1, g.Studies)
			assert.Equal(t, tc.want, model.EvidenceGroup{
				DoubleBlind: g.DoubleBlind, SingleBlind: g.SingleBlind, Open: g.Open,
				BlindingNotStated: g.BlindingNotStated, BlindingNotAssessed: g.BlindingNotAssessed,
			})
		})
	}
}

// Observational studies and reviews are never broken down by blinding, even
// when the critic gave one.
func TestEvidenceBaseBlindingOnlyForRandomized(t *testing.T) {
	eb := evidenceBaseEN(evState(
		evPaper{doi: "10.1/o", design: model.DesignCohort, blinding: model.BlindingOpen, size: 10},
	), nil)

	assert.Zero(t, eb.Observational.Open)
	assert.NotContains(t, eb.Summary, "open-label")
}

func TestEvidenceBaseSizes(t *testing.T) {
	trial := func(doi string, size, metaSize int) evPaper {
		return evPaper{doi: doi, title: "T" + doi, design: model.DesignRCT, size: size, metaSize: metaSize}
	}

	for _, tc := range []struct {
		name   string
		papers []evPaper
		want   model.EvidenceGroup // sizes only
		text   string
	}{
		{
			name:   "odd count",
			papers: []evPaper{trial("a", 100, 0), trial("b", 900, 0), trial("c", 300, 0)},
			want:   model.EvidenceGroup{Participants: 1300, MedianSize: 300, Largest: 900, LargestStudy: "Tb"},
			text:   "1300 participants, median 300, largest 900 — Tb",
		},
		{
			name:   "even count: the mean of the middle two, rounded half up",
			papers: []evPaper{trial("a", 100, 0), trial("b", 200, 0), trial("c", 301, 0), trial("d", 400, 0)},
			want:   model.EvidenceGroup{Participants: 1001, MedianSize: 251, Largest: 400, LargestStudy: "Td"},
			text:   "1001 participants, median 251, largest 400 — Td",
		},
		{
			name:   "the critic's count wins over the metadata's",
			papers: []evPaper{trial("a", 810, 1167), trial("b", 0, 400)},
			want:   model.EvidenceGroup{Participants: 1210, MedianSize: 605, Largest: 810, LargestStudy: "Ta"},
			text:   "1210 participants, median 605, largest 810 — Ta",
		},
		{
			name:   "some sizes unknown",
			papers: []evPaper{trial("a", 100, 0), trial("b", 0, 0), trial("c", 300, 0), trial("d", 0, 0)},
			want: model.EvidenceGroup{
				Participants: 400, SizeNotStated: 2, MedianSize: 200, Largest: 300, LargestStudy: "Tc",
			},
			text: "400 participants, median 200, largest 300 — Tc; size not stated for 2",
		},
		{
			name:   "one known size has no median and no largest to show",
			papers: []evPaper{trial("a", 250, 0), trial("b", 0, 0)},
			want:   model.EvidenceGroup{Participants: 250, SizeNotStated: 1, MedianSize: 250, Largest: 250, LargestStudy: "Ta"},
			text:   "250 participants — Ta; size not stated for 1",
		},
		{
			name:   "no size known",
			papers: []evPaper{trial("a", 0, 0), trial("b", 0, 0)},
			want:   model.EvidenceGroup{SizeNotStated: 2},
			text:   "size not stated for all 2",
		},
		{
			name:   "a single study without a size",
			papers: []evPaper{trial("a", 0, 0)},
			want:   model.EvidenceGroup{SizeNotStated: 1},
			text:   "size not stated",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := buildEvidenceBase(evState(tc.papers...), nil).Randomized
			require.NotNil(t, g)

			got := model.EvidenceGroup{
				Participants: g.Participants, SizeNotStated: g.SizeNotStated, MedianSize: g.MedianSize,
				Largest: g.Largest, LargestStudy: g.LargestStudy,
			}
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.text, strings.Join(sizeText(g), "; "))
		})
	}
}

func TestEvidenceBaseRepeatReportsCountOnce(t *testing.T) {
	for _, tc := range []struct {
		name        string
		papers      []evPaper
		wantStudies int
		wantRepeats int
		wantSize    int // of each study
	}{
		{
			name: "two reports of one trial: the later report is kept, with its size",
			papers: []evPaper{
				{doi: "10.1/12m", year: 2008, design: model.DesignRCT, size: 200, regs: []string{regEvidence}},
				{doi: "10.1/5y", year: 2013, design: model.DesignRCT, size: 150, regs: []string{regEvidence}},
			},
			wantStudies: 1, wantRepeats: 1, wantSize: 150,
		},
		{
			name: "three reports chained by two numbers are one trial",
			papers: []evPaper{
				{doi: doiA, year: 2008, design: model.DesignRCT, size: 100, regs: []string{regEvidence}},
				{doi: doiB, year: 2010, design: model.DesignRCT, size: 100, regs: []string{regEvidence, regEvidenceB}},
				{doi: doiC, year: 2012, design: model.DesignRCT, size: 100, regs: []string{regEvidenceB}},
			},
			wantStudies: 1, wantRepeats: 2, wantSize: 100,
		},
		{
			name: "the kept report states no size: the other report's is used",
			papers: []evPaper{
				{doi: doiA, year: 2008, design: model.DesignRCT, size: 320, regs: []string{regEvidence}},
				{doi: doiB, year: 2013, design: model.DesignRCT, regs: []string{regEvidence}},
			},
			wantStudies: 1, wantRepeats: 1, wantSize: 320,
		},
		{
			name: "the kept report's design is unclear: the other report's design is used",
			papers: []evPaper{
				{doi: doiA, year: 2008, design: model.DesignRCT, size: 100, regs: []string{regEvidence}},
				{doi: doiB, year: 2013, design: model.DesignUnclear, size: 100, regs: []string{regEvidence}},
			},
			wantStudies: 1, wantRepeats: 1, wantSize: 100,
		},
		{
			name: "different registrations are different trials",
			papers: []evPaper{
				{doi: doiA, design: model.DesignRCT, size: 100, regs: []string{regEvidence}},
				{doi: doiB, design: model.DesignRCT, size: 100, regs: []string{regEvidenceB}},
			},
			wantStudies: 2, wantSize: 100,
		},
		{
			name: "no registration number: never merged",
			papers: []evPaper{
				{doi: doiA, design: model.DesignRCT, size: 100},
				{doi: doiB, design: model.DesignRCT, size: 100},
			},
			wantStudies: 2, wantSize: 100,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eb := evidenceBaseEN(evState(tc.papers...), nil)
			require.NotNil(t, eb.Randomized)

			assert.Equal(t, tc.wantStudies, eb.Randomized.Studies)
			assert.Equal(t, tc.wantStudies, eb.Direct)
			assert.Equal(t, tc.wantRepeats, eb.RepeatReports)
			assert.Equal(t, tc.wantSize*tc.wantStudies, eb.Randomized.Participants)
			assert.Equal(t, len(tc.papers), eb.Reviewed)
			assert.Zero(t, eb.Other, "a trial with an unclear report still takes the design of its other report")

			if tc.wantRepeats > 0 {
				assert.Contains(t, eb.Summary, "repeat report")
			} else {
				assert.NotContains(t, eb.Summary, "repeat report")
			}
		})
	}
}

// A review that names the registration number of a trial it covers is not that
// trial: both are counted, and the review is never folded into the trial.
func TestEvidenceBaseReviewIsNotMergedIntoTrial(t *testing.T) {
	eb := buildEvidenceBase(evState(
		evPaper{doi: "10.1/t", design: model.DesignRCT, size: 100, regs: []string{regEvidence}},
		evPaper{doi: "10.1/r", design: model.DesignMetaAnalysis, size: 5000, regs: []string{regEvidence}},
	), nil)

	assert.Equal(t, 2, eb.Direct)
	assert.Zero(t, eb.RepeatReports)
	assert.Equal(t, 1, eb.Randomized.Studies)
	assert.Equal(t, 1, eb.Reviews.Studies)
}

func TestEvidenceBaseCochraneCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		dois []string
		want int
	}{
		{"a Cochrane review", []string{"10.1002/14651858.cd012345.pub2"}, 1},
		{"upper case and URL form", []string{"https://doi.org/10.1002/14651858.CD012345.PUB2"}, 1},
		{"another Wiley journal is not Cochrane", []string{"10.1002/jcsm.12345"}, 0},
		{"another publisher", []string{"10.1016/s0140-6736(20)30000-1"}, 0},
		{"two of three", []string{"10.1002/14651858.cd000001", "10.1/other", "10.1002/14651858.cd000002.pub3"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var papers []evPaper
			for _, d := range tc.dois {
				papers = append(papers, evPaper{doi: d, design: model.DesignSystematicReview})
			}

			eb := evidenceBaseEN(evState(papers...), nil)
			assert.Equal(t, tc.want, eb.Reviews.Cochrane)
			assert.Equal(t, len(tc.dois), eb.Reviews.Studies)

			if tc.want > 0 {
				assert.Contains(t, eb.Summary, "Cochrane")
			} else {
				assert.NotContains(t, eb.Summary, "Cochrane")
			}
		})
	}
}

// A retracted paper is left out of the breakdown and said so; an erratum is
// not a study and counts with the off-topic papers.
func TestEvidenceBaseRetractedAndNotStudies(t *testing.T) {
	eb := evidenceBaseEN(evState(
		evPaper{doi: doiA, design: model.DesignRCT, size: 100},
		evPaper{doi: doiB, design: model.DesignRCT, size: 999, retracted: true},
		evPaper{doi: doiC, design: model.DesignRCT, types: []string{typeErratumEv}},
	), nil)

	assert.Equal(t, 1, eb.Direct)
	assert.Equal(t, 1, eb.Retracted)
	assert.Equal(t, 1, eb.Indirect)
	assert.Equal(t, 100, eb.Randomized.Participants)
	assert.Contains(t, eb.Summary, "Retracted, left out: 1.")
	assert.True(t, strings.HasSuffix(eb.Summary, "Indirect or off-topic: 1."))
}

// A paper whose critique failed, or that has no critique, is neither reviewed
// nor indirect: it is counted apart, and Reviewed stays the sum of the rest.
func TestEvidenceBaseNotAssessed(t *testing.T) {
	assertSum := func(t *testing.T, eb *model.EvidenceBase) {
		t.Helper()

		assert.Equal(t, eb.Direct+eb.RepeatReports+eb.Retracted+eb.Indirect, eb.Reviewed)
	}

	t.Run("a failed critique", func(t *testing.T) {
		eb := evidenceBaseEN(evState(
			evPaper{doi: doiA, design: model.DesignRCT, size: 100, regs: []string{regEvidence}},
			evPaper{doi: doiB, design: model.DesignRCT, size: 100, regs: []string{regEvidence}},
			evPaper{doi: doiC, relevance: relevanceOff},
			evPaper{doi: doiX, failed: true},
			evPaper{doi: doiY, failed: true, retracted: true},
		), nil)

		assert.Equal(t, 2, eb.NotAssessed)
		assert.Equal(t, 3, eb.Reviewed)
		assert.Equal(t, 1, eb.Direct)
		assert.Equal(t, 1, eb.RepeatReports)
		assert.Equal(t, 1, eb.Indirect, "the failed papers are not off-topic")
		assert.Zero(t, eb.Retracted, "nothing is known about a failed paper, retracted or not")
		assertSum(t, eb)
		assert.True(t, strings.HasPrefix(eb.Summary, "3 papers reviewed; 1 answers"), eb.Summary)
		assert.True(t, strings.HasSuffix(eb.Summary,
			"Indirect or off-topic: 1. Not assessed (critique failed): 2."), eb.Summary)
	})

	t.Run("no critique matched the paper", func(t *testing.T) {
		st := evState(
			evPaper{doi: doiA, design: model.DesignRCT, size: 100},
			evPaper{doi: doiB, design: model.DesignRCT, size: 100},
		)
		st.Critiques = st.Critiques[:1] // fewer critiques than papers: matched by DOI

		eb := buildEvidenceBase(st, nil)

		assert.Equal(t, 1, eb.NotAssessed)
		assert.Equal(t, 1, eb.Reviewed)
		assert.Equal(t, 1, eb.Direct)
		assert.Zero(t, eb.Indirect)
		assertSum(t, eb)
	})

	t.Run("none failed: nothing is said", func(t *testing.T) {
		eb := evidenceBaseEN(evState(evPaper{doi: doiA, design: model.DesignRCT}), nil)

		assert.Zero(t, eb.NotAssessed)
		assert.NotContains(t, eb.Summary, "Not assessed")

		raw, err := json.Marshal(eb)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), `"not_assessed"`)
	})

	t.Run("all failed", func(t *testing.T) {
		eb := evidenceBaseEN(evState(evPaper{doi: doiA, failed: true}, evPaper{doi: doiB, failed: true}), nil)

		assert.Zero(t, eb.Reviewed)
		assert.Equal(t, 2, eb.NotAssessed)
		assert.Equal(t, "0 papers reviewed; none answer the question directly. "+noPoolText+
			" Indirect or off-topic: 0. Not assessed (critique failed): 2.", eb.Summary)
	})
}

func TestEvidenceBaseNothingDirect(t *testing.T) {
	eb := evidenceBaseEN(evState(
		evPaper{doi: doiA, relevance: relevanceIndirect, design: model.DesignRCT},
		evPaper{doi: doiB, relevance: relevanceOff},
	), nil)

	assert.Zero(t, eb.Direct)
	assert.Nil(t, eb.Randomized)
	assert.Equal(t, "2 papers reviewed; none answer the question directly. "+
		"Not pooled: no direct study with usable numbers on the outcome. Indirect or off-topic: 2.", eb.Summary)
}

func TestEvidenceBaseWithoutPapers(t *testing.T) {
	assert.Nil(t, buildEvidenceBase(&State{}, nil))
}

// A design that only the discovery tag or the title tells still counts.
func TestEvidenceBaseUsesTheResolvedDesign(t *testing.T) {
	st := evState(
		evPaper{doi: doiA, title: "Zinc for colds: a systematic review"},
		evPaper{doi: doiB},
	)
	st.Enriched[1].StudyDesign = "cohort study"

	eb := buildEvidenceBase(st, nil)
	assert.Equal(t, 1, eb.Reviews.Studies)
	assert.Equal(t, 1, eb.Observational.Studies)
}

func TestEvidenceBaseLargestLabel(t *testing.T) {
	long := strings.Repeat("long ", 20)

	for _, tc := range []struct {
		name  string
		paper evPaper
		want  string
	}{
		{"a short title", evPaper{title: "Short title", size: 10}, "Short title"},
		{"a long title is cut", evPaper{title: long, size: 10}, strings.TrimSpace(long)[:59] + "…"},
		{
			"no title: the first author and the year",
			evPaper{title: " ", authors: []string{"Smith J", "Doe A"}, year: 2021, size: 10},
			"Smith J 2021",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.paper
			p.doi, p.design = doiX, model.DesignRCT

			st := evState(p)
			st.Enriched[0].Title = p.title

			assert.Equal(t, tc.want, buildEvidenceBase(st, nil).Randomized.LargestStudy)
		})
	}

	t.Run("no title and no author: the DOI", func(t *testing.T) {
		st := evState(evPaper{doi: doiX, design: model.DesignRCT, size: 10})
		st.Enriched[0].Title = ""

		assert.Equal(t, doiX, buildEvidenceBase(st, nil).Randomized.LargestStudy)
	})
}

func TestEvidencePooledSentence(t *testing.T) {
	for _, tc := range []struct {
		name string
		pe   *model.PooledEstimate
		want string
	}{
		{"nothing pooled", nil, noPoolText},
		{
			"randomized trials with participants and events",
			&model.PooledEstimate{
				Method: model.PoolRandomEffects, Stratum: model.StratumRandomized, K: 4, Participants: 2010, Events: 310,
			},
			"Pooled: 4 trials, 2010 participants, 310 events.",
		},
		{
			"non-randomized studies, participants unknown",
			&model.PooledEstimate{Method: model.PoolRandomEffects, Stratum: model.StratumNonRandomized, K: 3, Events: 80},
			"Pooled: 3 studies, 80 events.",
		},
		{
			"a pool of unknown design and no counts",
			&model.PooledEstimate{Method: model.PoolRandomEffects, K: 2},
			"Pooled: 2 studies.",
		},
		{
			"one study is not pooled",
			&model.PooledEstimate{
				Method: model.PoolSingleStudy, Stratum: model.StratumRandomized, K: 1, Participants: 380, Events: 40,
			},
			"Not pooled: one study with numbers (380 participants, 40 events).",
		},
		{
			"one study without counts",
			&model.PooledEstimate{Method: model.PoolSingleStudy, K: 1},
			"Not pooled: one study with numbers.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, pooledSentence(tc.pe))
		})
	}
}

// The example of the feature: five trials with a blinding breakdown, observational
// studies, reviews and a pool.
func TestEvidenceTextExample(t *testing.T) {
	eb := &model.EvidenceBase{
		Reviewed: 40, Direct: 12, Indirect: 28,
		Randomized: &model.EvidenceGroup{
			Studies: 5, DoubleBlind: 3, Open: 1, BlindingNotStated: 1,
			Participants: 2310, MedianSize: 380, Largest: 1105, LargestStudy: "Big trial",
		},
		Observational: &model.EvidenceGroup{
			Studies: 4, Participants: 18400, MedianSize: 3000, Largest: 9800, LargestStudy: "Big cohort",
		},
		Reviews: &model.EvidenceGroup{Studies: 3, Cochrane: 1},
	}
	pe := &model.PooledEstimate{
		Method: model.PoolRandomEffects, Stratum: model.StratumRandomized, K: 4, Participants: 2010, Events: 310,
	}

	assert.Equal(t, "40 papers reviewed; 12 answer the question directly: "+
		"5 randomized trials (3 double-blind, 1 open-label, 1 blinding not stated; "+
		"2310 participants, median 380, largest 1105 — Big trial), "+
		"4 observational studies (18400 participants, median 3000, largest 9800 — Big cohort), "+
		"3 systematic reviews or meta-analyses (1 Cochrane). "+
		"Pooled: 4 trials, 2010 participants, 310 events. Indirect or off-topic: 28.", evidenceText(eb, pe))

	eb.Reviewed, eb.NotAssessed = 38, 2
	assert.True(t, strings.HasSuffix(evidenceText(eb, pe),
		"Indirect or off-topic: 28. Not assessed (critique failed): 2."))
}

func TestEvidenceTextSingulars(t *testing.T) {
	eb := &model.EvidenceBase{
		Reviewed: 1, Direct: 1,
		Reviews: &model.EvidenceGroup{Studies: 1},
	}
	assert.Equal(t, "1 paper reviewed; 1 answers the question directly: 1 systematic review or meta-analysis. "+
		noPoolText+" Indirect or off-topic: 0.", evidenceText(eb, nil))

	eb = &model.EvidenceBase{
		Reviewed: 3, Direct: 2, Other: 1, Observational: &model.EvidenceGroup{Studies: 1, SizeNotStated: 1},
	}
	assert.Contains(t, evidenceText(eb, nil),
		"1 observational study (size not stated), 1 other publication (narrative reviews")
}

func TestMedian(t *testing.T) {
	assert.Zero(t, median(nil))
	assert.Equal(t, 5, median([]int{5}))
	assert.Equal(t, 3, median([]int{5, 1, 3}))
	assert.Equal(t, 3, median([]int{4, 2}), "the mean of two, rounded half up")
}

// The summary rides in the report artifact and is left out of an old one.
func TestEvidenceBaseJSON(t *testing.T) {
	st := evState(evPaper{doi: doiA, design: model.DesignRCT, size: 100})
	r := model.Report{Verdict: verdictWeakEv, EvidenceBase: buildEvidenceBase(st, nil)}

	b, err := json.Marshal(r)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"evidence_base":{"reviewed":1,"direct":1,"indirect":0,"randomized":{"studies":1,`)

	var back model.Report

	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, r.EvidenceBase, back.EvidenceBase)

	old, err := json.Marshal(model.Report{Verdict: verdictWeakEv})
	require.NoError(t, err)
	assert.NotContains(t, string(old), "evidence_base")
}

// An old artifact has no evidence base: the report renders without the line.
func TestRenderWithoutEvidenceBase(t *testing.T) {
	st := reportState()
	require.NotNil(t, st.Report.EvidenceBase)
	assert.Contains(t, RenderMarkdown(st), "**Evidence base:** 3 papers reviewed")

	st.Report.EvidenceBase = nil
	assert.NotContains(t, RenderMarkdown(st), "Evidence base")
}
