package pipeline

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// The three reports of the STAR trial that were pooled as k=3 (question 149):
// the journal paper, a reprint of its abstract and a conference abstract. None
// has a registration number, each gives only the reported RR.
const (
	doiSTARJournal = "10.1001/jama.295.23.joc60074"
	doiSTARReprint = "10.1097/01.ogx.0000238648.23538.42"
	doiSTARASCO    = "10.1200/jco.2006.24.18_suppl.lba5"
	starSize       = 19747
)

// star is one STAR report: an RR of 1.02 with the interval given.
func star(doi string, year int, low, high float64, size int) poolStudy {
	return poolStudy{
		doi: doi, year: year, design: model.DesignRCT, sizeReported: size,
		effect: reported("RR", 1.02, low, high),
	}
}

// Two reports that give the same estimate for the same number of participants
// are one result seen twice; the rest stay separate studies.
func TestPoolDuplicatesByReportedEstimate(t *testing.T) {
	const other = "10.1/other-trial"

	for _, tc := range []struct {
		name        string
		studies     []poolStudy
		wantStudies []string
		wantLeft    []string
	}{
		{
			name: "STAR: three reports, one interval rounded differently",
			studies: []poolStudy{
				star(doiSTARASCO, 2006, 0.82, 1.27, starSize),
				star(doiSTARReprint, 2007, 0.82, 1.28, starSize),
				star(doiSTARJournal, 2006, 0.82, 1.28, starSize),
			},
			// the same year as the ASCO abstract: the lower DOI
			wantStudies: []string{doiSTARJournal},
			wantLeft: []string{
				doiSTARASCO + " (same results as " + doiSTARJournal + ")",
				doiSTARReprint + " (same results as " + doiSTARJournal + ")",
			},
		},
		{
			name: "the critic's count decides, not the metadata's",
			studies: []poolStudy{
				{
					doi: doiSTARJournal, year: 2006, design: model.DesignRCT, size: 19000, sizeReported: starSize,
					effect: reported("RR", 1.02, 0.82, 1.28),
				},
				{
					doi: doiSTARReprint, year: 2007, design: model.DesignRCT, size: starSize,
					effect: reported("RR", 1.02, 0.82, 1.28),
				},
			},
			wantStudies: []string{doiSTARJournal},
			wantLeft:    []string{doiSTARReprint + " (same results as " + doiSTARJournal + ")"},
		},
		{
			name: "the same estimate in trials of different sizes",
			studies: []poolStudy{
				star(doiSTARJournal, 2006, 0.82, 1.28, starSize),
				star(other, 2008, 0.82, 1.28, 400),
			},
			wantStudies: []string{doiSTARJournal, other},
		},
		{
			name: "the participants of both are unknown",
			studies: []poolStudy{
				star(doiSTARJournal, 2006, 0.82, 1.28, 0),
				star(doiSTARReprint, 2007, 0.82, 1.28, 0),
			},
			wantStudies: []string{doiSTARJournal, doiSTARReprint},
		},
		{
			name: "the participants of one are unknown",
			studies: []poolStudy{
				star(doiSTARJournal, 2006, 0.82, 1.28, starSize),
				star(doiSTARReprint, 2007, 0.82, 1.28, 0),
			},
			wantStudies: []string{doiSTARJournal, doiSTARReprint},
		},
		{
			name: "an interval bound that differs by 0.02",
			studies: []poolStudy{
				star(doiSTARJournal, 2006, 0.82, 1.28, starSize),
				star(doiSTARReprint, 2007, 0.82, 1.30, starSize),
			},
			wantStudies: []string{doiSTARJournal, doiSTARReprint},
		},
		{
			name: "the lower bound differs by 0.02",
			studies: []poolStudy{
				star(doiSTARJournal, 2006, 0.82, 1.28, starSize),
				star(doiSTARReprint, 2007, 0.80, 1.28, starSize),
			},
			wantStudies: []string{doiSTARJournal, doiSTARReprint},
		},
		{
			name: "another point value",
			studies: []poolStudy{
				star(doiSTARJournal, 2006, 0.82, 1.28, starSize),
				{
					doi: doiSTARReprint, year: 2007, design: model.DesignRCT, sizeReported: starSize,
					effect: reported("RR", 1.03, 0.82, 1.28),
				},
			},
			wantStudies: []string{doiSTARJournal, doiSTARReprint},
		},
		{
			name: "another measure",
			studies: []poolStudy{
				star(doiSTARJournal, 2006, 0.82, 1.28, starSize),
				{
					doi: doiSTARReprint, year: 2007, design: model.DesignRCT, sizeReported: starSize,
					effect: reported("OR", 1.02, 0.82, 1.28),
				},
			},
			wantStudies: []string{doiSTARJournal}, // the OR is left out of the RR pool, not as a duplicate
		},
		{
			name: "arm data are not compared with an estimate",
			studies: []poolStudy{
				star(doiSTARJournal, 2006, 0.82, 1.28, starSize),
				{
					doi: doiSTARReprint, year: 2007, design: model.DesignRCT, sizeReported: starSize,
					effect: counts(100, 9873, 98, 9874),
				},
			},
			wantStudies: []string{doiSTARJournal, doiSTARReprint},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, logs := poolPipeline(t)

			pe := p.poolEffects(poolCorpus(tc.studies...))

			require.NotNil(t, pe)
			assert.ElementsMatch(t, tc.wantStudies, pe.Studies)

			if tc.wantLeft != nil {
				assert.Equal(t, tc.wantLeft, pe.LeftOut)
				assert.Contains(t, logs.String(), `"effects not pooled" reason="same results"`)

				return
			}

			for _, left := range pe.LeftOut {
				assert.NotContains(t, left, leftSameResults)
			}
		})
	}
}

// chainStudy is a reported RR 1 with a lower bound of 0.5 and the given upper
// bound, of 100 participants.
func chainStudy(doi string, year int, high float64) pooledStudy {
	return pooledStudy{
		doi: doi, year: year, n: 100, measure: model.MeasureRR,
		estimate: &reportedEstimate{value: 1, low: 0.5, high: high},
	}
}

// permutations returns every ordering of 0..n-1.
func permutations(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}

	var out [][]int

	for _, rest := range permutations(n - 1) {
		for at := 0; at <= len(rest); at++ {
			p := slices.Insert(slices.Clone(rest), at, n-1)
			out = append(out, p)
		}
	}

	return out
}

// The tolerance of the interval bounds is not transitive: A is close to B and B
// to C, but A is not close to C. The groups are built in the order of the
// keeper preference (the earlier year first), so A keeps its group, B joins it
// (B is processed before C and matches A), and C, which does not match A, is a
// study of its own. The result is the same for every order of the input.
func TestDropDuplicatesToleranceIsNotChained(t *testing.T) {
	studies := []pooledStudy{
		chainStudy("10.1/a", 2005, 1.000), chainStudy("10.1/b", 2006, 1.008), chainStudy("10.1/c", 2007, 1.016),
	}

	for _, order := range permutations(len(studies)) {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			in := make([]pooledStudy, 0, len(order))
			for _, i := range order {
				in = append(in, studies[i])
			}

			kept, dropped := dropDuplicates(in)

			// kept and dropped keep the order of the input
			assert.Equal(t, wantInOrder(in, "10.1/a", "10.1/c"), doisOf(kept))
			require.Len(t, dropped, 1)
			assert.Equal(t, "10.1/b (same results as 10.1/a)", dropped[0].text)
			assert.Equal(t, leftSameResults, dropped[0].kind)
		})
	}
}

// wantInOrder is the DOIs of want in the order the input lists them.
func wantInOrder(in []pooledStudy, want ...string) []string {
	var out []string

	for i := range in {
		if slices.Contains(want, in[i].doi) {
			out = append(out, in[i].doi)
		}
	}

	return out
}

func doisOf(items []pooledStudy) []string {
	out := make([]string, 0, len(items))
	for i := range items {
		out = append(out, items[i].doi)
	}

	return out
}

// A full article is kept over a conference abstract of the same results, even
// when the abstract came out a year earlier; among two of a kind the earlier
// year decides. None of it depends on the order of the input.
func TestDropDuplicatesKeepsAnArticleOverAnAbstract(t *testing.T) {
	abstractFirst := chainStudy("10.1/abstract", 2005, 1.000)
	abstractFirst.abstract = true

	studies := []pooledStudy{
		abstractFirst, chainStudy("10.1/article", 2006, 1.000), chainStudy("10.1/reprint", 2007, 1.000),
	}

	for _, order := range permutations(len(studies)) {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			in := make([]pooledStudy, 0, len(order))
			for _, i := range order {
				in = append(in, studies[i])
			}

			kept, dropped := dropDuplicates(in)

			require.Len(t, kept, 1)
			assert.Equal(t, "10.1/article", kept[0].doi)
			assert.ElementsMatch(t, []string{
				"10.1/abstract (same results as 10.1/article)",
				"10.1/reprint (same results as 10.1/article)",
			}, leftTexts(dropped))
		})
	}
}

// Only abstracts of the group: the earlier one is kept as before.
func TestDropDuplicatesAmongAbstractsTheEarlierIsKept(t *testing.T) {
	early, late := chainStudy("10.1/early", 2005, 1.000), chainStudy("10.1/late", 2006, 1.000)
	early.abstract, late.abstract = true, true

	kept, _ := dropDuplicates([]pooledStudy{late, early})

	assert.Equal(t, []string{"10.1/early"}, doisOf(kept))
}

func leftTexts(items []leftOut) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.text)
	}

	return out
}

// The abstract flag comes from the publication types of the paper (PubMed's
// Congress, Crossref's proceedings-article): the abstract of a year earlier is
// left out, the article kept.
func TestPoolKeepsTheArticleOverAConferenceAbstract(t *testing.T) {
	const doiAbstract = "10.1200/jco.2005.23.16_suppl.lba1"

	for _, types := range [][]string{{"Congress"}, {"proceedings-article"}, {"Meeting Abstracts"}} {
		t.Run(types[0], func(t *testing.T) {
			p, _ := poolPipeline(t)

			abstract := star(doiAbstract, 2005, 0.82, 1.28, starSize)
			abstract.pubTypes = types

			pe := p.poolEffects(poolCorpus(abstract, star(doiSTARJournal, 2006, 0.82, 1.28, starSize)))

			require.NotNil(t, pe)
			assert.Equal(t, []string{doiSTARJournal}, pe.Studies)
			assert.Equal(t, []string{doiAbstract + " (same results as " + doiSTARJournal + ")"}, pe.LeftOut)
		})
	}
}

// Two reports that both give a reversed estimate (control vs intervention) are
// compared on the reversed form, which is the same for the same numbers, so
// they are one result seen twice.
func TestPoolDuplicatesOfReversedEstimates(t *testing.T) {
	reversed := func() *model.Effect {
		ef := reported("RR", 0.98, 0.80, 1.20)
		ef.Reversed = true

		return ef
	}

	p, _ := poolPipeline(t)

	pe := p.poolEffects(poolCorpus(
		poolStudy{doi: doiSTARJournal, year: 2006, design: model.DesignRCT, sizeReported: starSize, effect: reversed()},
		poolStudy{doi: doiSTARReprint, year: 2007, design: model.DesignRCT, sizeReported: starSize, effect: reversed()},
	))

	require.NotNil(t, pe)
	assert.Equal(t, []string{doiSTARJournal}, pe.Studies)
	assert.Equal(t, []string{doiSTARReprint + " (same results as " + doiSTARJournal + ")"}, pe.LeftOut)
}

// Current behaviour, not a requirement: a reversed 0.98 is 1/0.98 = 1.0204 in
// the pooled form, and the point value is compared exactly, so it does not
// match a directly reported 1.02 of the same trial. The two reports are not
// merged.
func TestPoolDoesNotMergeAReversedEstimateWithADirectOneRoundedApart(t *testing.T) {
	reversed := reported("RR", 0.98, 0.80, 1.20)
	reversed.Reversed = true

	p, _ := poolPipeline(t)

	pe := p.poolEffects(poolCorpus(
		poolStudy{doi: doiSTARJournal, year: 2006, design: model.DesignRCT, sizeReported: starSize, effect: reversed},
		poolStudy{
			doi: doiSTARReprint, year: 2007, design: model.DesignRCT, sizeReported: starSize,
			effect: reported("RR", 1.02, 1/1.20, 1/0.80),
		},
	))

	require.NotNil(t, pe)
	assert.ElementsMatch(t, []string{doiSTARJournal, doiSTARReprint}, pe.Studies)
	assert.Empty(t, pe.LeftOut)
}

func TestSameResultsBoundAtTheTolerance(t *testing.T) {
	at := func(low, high float64) *pooledStudy {
		return &pooledStudy{
			n: 100, measure: model.MeasureRR, estimate: &reportedEstimate{value: 1.02, low: low, high: high},
		}
	}

	assert.True(t, sameResults(at(0.82, 1.28), at(0.82, 1.27)), "0.01 apart is rounding")
	assert.True(t, sameResults(at(0.82, 1.28), at(0.83, 1.28)))
	assert.False(t, sameResults(at(0.82, 1.28), at(0.82, 1.26)))
	assert.False(t, sameResults(at(0.82, 1.28), at(0.80, 1.28)))
}
