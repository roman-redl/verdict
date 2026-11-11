package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// fakeTrials is the PubMed registration lookup: canned accession numbers per
// DOI, or one error for every call.
type fakeTrials struct {
	calls   counter
	asked   [][]string // the DOIs of each call
	numbers map[string][]string
	err     error
}

var _ TrialRegistry = (*fakeTrials)(nil)

func (f *fakeTrials) Registrations(_ context.Context, dois []string) (map[string][]string, error) {
	f.calls.add("Registrations")
	f.asked = append(f.asked, dois)

	if f.err != nil {
		return map[string][]string{}, f.err
	}

	out := map[string][]string{}

	for _, d := range dois {
		if n, ok := f.numbers[d]; ok {
			out[d] = n
		}
	}

	return out, nil
}

func TestTrialGroups(t *testing.T) {
	primary := func(ids ...string) trialPaper { return trialPaper{ids: ids, primary: true} }
	review := func(ids ...string) trialPaper { return trialPaper{ids: ids} }

	for _, tc := range []struct {
		name   string
		papers []trialPaper
		want   []int
	}{
		{"no papers", nil, []int{}},
		{"no registration numbers", []trialPaper{primary(), primary(), primary()}, []int{0, 1, 2}},
		{"different numbers", []trialPaper{primary("A"), primary("B")}, []int{0, 1}},
		{"a shared number", []trialPaper{primary("A"), primary("B"), primary("A")}, []int{0, 1, 0}},
		{"two trials interleaved", []trialPaper{primary("A"), primary("B"), primary("A"), primary("B")}, []int{0, 1, 0, 1}},
		{"a chain through different numbers", []trialPaper{primary("A"), primary("A", "B"), primary("B")}, []int{0, 0, 0}},
		{"the chain in the other order", []trialPaper{primary("B"), primary("A", "B"), primary("A")}, []int{0, 0, 0}},
		{
			"a paper that bridges two groups formed earlier",
			[]trialPaper{primary("A"), primary("B"), primary("A", "B")},
			[]int{0, 0, 0},
		},
		{
			"a long chain",
			[]trialPaper{primary("D"), primary("C", "D"), primary("B", "C"), primary("A", "B")},
			[]int{0, 0, 0, 0},
		},
		{"a review never joins", []trialPaper{primary("A"), review("A"), primary("A")}, []int{0, 1, 0}},
		{"a review does not bridge two trials", []trialPaper{primary("A"), review("A", "B"), primary("B")}, []int{0, 1, 2}},
		{"the group is named by its first paper", []trialPaper{primary(), primary("A"), primary("A")}, []int{0, 1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, trialGroups(tc.papers))
		})
	}
}

func TestReportRankKeptOver(t *testing.T) {
	rk := func(doi string, year, n int) reportRank { return reportRank{doi: doi, year: year, n: n} }

	for _, tc := range []struct {
		name string
		a, b reportRank
		want bool
	}{
		{"the later year", rk("z", 2013, 0), rk("a", 2008, 900), true},
		{"the earlier year", rk("a", 2008, 900), rk("z", 2013, 0), false},
		{"an unknown year", rk("a", 0, 0), rk("z", 2008, 0), false},
		{"the same year, more participants", rk("z", 2013, 300), rk("a", 2013, 200), true},
		{"the same year, fewer participants", rk("a", 2013, 200), rk("z", 2013, 300), false},
		{"all equal, the lower DOI", rk("a", 2013, 200), rk("b", 2013, 200), true},
		{"all equal, the higher DOI", rk("b", 2013, 200), rk("a", 2013, 200), false},
		{"the same report", rk("a", 2013, 0), rk("a", 2013, 0), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.a.keptOver(tc.b))
		})
	}
}

func TestSharedRegistration(t *testing.T) {
	assert.Equal(t, "B", sharedRegistration([]string{"A", "B"}, []string{"B", "C"}))
	assert.Equal(t, "A", sharedRegistration([]string{"A", "B"}, []string{"C"}),
		"linked through a third report: the dropped one's first")
	assert.Empty(t, sharedRegistration(nil, []string{"C"}))
}

// fillTestPipeline builds a pipeline whose log goes into the returned buffer.
func fillTestPipeline(t *testing.T, trials TrialRegistry) (*Pipeline, *bytes.Buffer) {
	t.Helper()

	var logs bytes.Buffer

	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	return newTest(t, Settings{}, Clients{Trials: trials}, log), &logs
}

func TestFillTrialRegistrations(t *testing.T) {
	paper := func(doi, abstract string) model.EnrichedPaper {
		e := ep(doi)
		e.Abstract = abstract

		return e
	}

	t.Run("the abstract names the number: PubMed is not asked", func(t *testing.T) {
		ft := &fakeTrials{numbers: map[string][]string{doiA: {"NCT99999999"}}}
		p, _ := fillTestPipeline(t, ft)
		enriched := []model.EnrichedPaper{paper(doiA, "Methods. Trial registration: ISRCTN15517081.")}

		p.fillTrialRegistrations(t.Context(), enriched)

		assert.Equal(t, []string{regReflux}, enriched[0].TrialRegistrations)
		assert.Zero(t, ft.calls.total())
	})

	t.Run("PubMed fills the papers whose abstract names none", func(t *testing.T) {
		ft := &fakeTrials{numbers: map[string][]string{
			doiA: {regNCT, "AB123456"}, // a GenBank accession is not a trial registration
			doiB: {"AB123456"},
		}}
		p, logs := fillTestPipeline(t, ft)
		enriched := []model.EnrichedPaper{
			paper(doiA, "No number in this one."),
			paper(doiB, "Nor in this one."),
			paper(doiC, "Registered at NCT00000003."),
			paper("", "No DOI to look up."),
		}

		p.fillTrialRegistrations(t.Context(), enriched)

		assert.Equal(t, []string{regNCT}, enriched[0].TrialRegistrations)
		assert.Empty(t, enriched[1].TrialRegistrations)
		assert.Equal(t, []string{"NCT00000003"}, enriched[2].TrialRegistrations)
		assert.Empty(t, enriched[3].TrialRegistrations)
		require.Len(t, ft.asked, 1, "one batched lookup")
		assert.Equal(t, []string{doiA, doiB}, ft.asked[0])
		assert.Contains(t, logs.String(), "filled=1")
	})

	t.Run("a PubMed failure only logs: the abstract numbers stay", func(t *testing.T) {
		ft := &fakeTrials{err: errors.New("pubmed is down")}
		p, logs := fillTestPipeline(t, ft)
		enriched := []model.EnrichedPaper{
			paper(doiA, "No number."),
			paper(doiB, regReflux),
		}

		p.fillTrialRegistrations(t.Context(), enriched)

		assert.Empty(t, enriched[0].TrialRegistrations)
		assert.Equal(t, []string{regReflux}, enriched[1].TrialRegistrations)
		assert.Contains(t, logs.String(), "level=WARN")
		assert.Contains(t, logs.String(), "pubmed is down")
	})

	t.Run("a review is not looked up", func(t *testing.T) {
		ft := &fakeTrials{numbers: map[string][]string{doiA: {regNCT}}}
		p, _ := fillTestPipeline(t, ft)
		review := paper(doiA, "No number.")
		review.StudyDesign = discTagMeta
		enriched := []model.EnrichedPaper{review}

		p.fillTrialRegistrations(t.Context(), enriched)

		assert.Empty(t, enriched[0].TrialRegistrations)
		assert.Zero(t, ft.calls.total())
	})

	t.Run("without the PubMed port the abstracts are enough", func(t *testing.T) {
		p, _ := fillTestPipeline(t, nil)
		enriched := []model.EnrichedPaper{paper(doiA, regReflux), paper(doiB, "none")}

		p.fillTrialRegistrations(t.Context(), enriched)

		assert.Equal(t, []string{regReflux}, enriched[0].TrialRegistrations)
		assert.Empty(t, enriched[1].TrialRegistrations)
	})
}

// The enrichment stage fills the field and the artifact carries it.
func TestEnrichStoresTrialRegistrations(t *testing.T) {
	ft := &fakeTrials{numbers: map[string][]string{doiB: {regReflux}}}
	p := newTest(t, Settings{Concurrency: 1}, Clients{Citations: &fakeCitations{}, Trials: ft}, quietLog())

	st := &State{Papers: []model.Paper{
		{DOI: doiA, Abstract: "Trial registration: NCT01234567.", Source: sourceManual},
		{DOI: doiB, Abstract: "An abstract with no number.", Source: sourceManual},
	}}

	// the enrichment cache lands next to the run directory, so the run lives in a fresh one
	require.NoError(t, p.enrich(t.Context(), params(Options{RunDir: filepath.Join(t.TempDir(), "run")}), st))

	assert.Equal(t, []string{regNCT}, st.Enriched[0].TrialRegistrations)
	assert.Equal(t, []string{regReflux}, st.Enriched[1].TrialRegistrations)
	assert.Equal(t, [][]string{{doiB}}, ft.asked)

	b, err := json.Marshal(st)
	require.NoError(t, err)

	var back State
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, st.Enriched[1].TrialRegistrations, back.Enriched[1].TrialRegistrations, "the artifact round-trips")
}

// tallyPaper is one study of the tally tests.
type tallyPaper struct {
	doi       string
	year, n   int
	ids       []string
	relevance string
	direction string
	design    model.StudyDesign
}

func tallyState(papers ...tallyPaper) *State {
	st := &State{}

	for _, tp := range papers {
		e := ep(tp.doi)
		e.Year, e.SampleSize, e.TrialRegistrations = tp.year, tp.n, tp.ids
		e.Abstract = "An abstract with no number."

		c := crit(tp.doi, qualityHigh, tp.relevance, tp.direction)
		c.StudyDesign = tp.design

		st.Enriched = append(st.Enriched, e)
		st.Critiques = append(st.Critiques, c)
	}

	return st
}

func TestDirectionTallyCountsATrialOnce(t *testing.T) {
	reg := []string{regReflux}

	for _, tc := range []struct {
		name   string
		papers []tallyPaper
		want   string
	}{
		{
			name: "two reports of one trial: the latest direction",
			papers: []tallyPaper{
				{doi: doiOld, year: 2008, ids: reg, relevance: relevanceDirect, direction: directionPositive},
				{doi: doiNew, year: 2013, ids: reg, relevance: relevanceDirect, direction: directionNegative},
				{doi: doiOther, year: 2010, relevance: relevanceDirect, direction: directionNull},
			},
			want: "1 null / 1 negative",
		},
		{
			name: "the corpus order does not matter",
			papers: []tallyPaper{
				{doi: doiOther, year: 2010, relevance: relevanceDirect, direction: directionNull},
				{doi: doiNew, year: 2013, ids: reg, relevance: relevanceDirect, direction: directionNegative},
				{doi: doiOld, year: 2008, ids: reg, relevance: relevanceDirect, direction: directionPositive},
			},
			want: "1 null / 1 negative",
		},
		{
			name: "the latest report is not direct: the latest direct one counts",
			papers: []tallyPaper{
				{doi: doiOld, year: 2008, ids: reg, relevance: relevanceDirect, direction: directionPositive},
				{doi: "10.1/hta", year: 2015, ids: reg, relevance: relevanceIndirect, direction: directionNegative},
			},
			want: "1 positive",
		},
		{
			name: "the same year: the larger report",
			papers: []tallyPaper{
				{doi: "10.1/small", year: 2013, n: 100, ids: reg, relevance: relevanceDirect, direction: directionPositive},
				{doi: "10.1/large", year: 2013, n: 900, ids: reg, relevance: relevanceDirect, direction: directionNull},
			},
			want: "1 null",
		},
		{
			name: "papers without registration numbers count as they did",
			papers: []tallyPaper{
				{doi: doiA, year: 2008, relevance: relevanceDirect, direction: directionPositive},
				{doi: doiB, year: 2013, relevance: relevanceDirect, direction: directionPositive},
			},
			want: "2 positive",
		},
		{
			name: "different numbers are different trials",
			papers: []tallyPaper{
				{doi: doiA, ids: []string{regNCT1}, relevance: relevanceDirect, direction: directionPositive},
				{doi: doiB, ids: []string{regNCT2}, relevance: relevanceDirect, direction: directionNegative},
			},
			want: "1 positive / 1 negative",
		},
		{
			name: "a review sharing the number is counted on its own, as before",
			papers: []tallyPaper{
				{doi: "10.1/trial", year: 2013, ids: reg, relevance: relevanceDirect, direction: directionPositive},
				{
					doi: "10.1/review", year: 2020, ids: reg, relevance: relevanceDirect, direction: directionNegative,
					design: model.DesignSystematicReview,
				},
			},
			want: "1 positive / 1 negative",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, directionTally(tallyState(tc.papers...)))
		})
	}
}

// The registration numbers reach the synthesis input and the digest, so that
// the model sees which reports share a trial.
func TestSynthesisInputCarriesTheTrialRegistrations(t *testing.T) {
	st := tallyState(
		tallyPaper{
			doi: doiOld, year: 2008, ids: []string{regReflux},
			relevance: relevanceDirect, direction: directionPositive,
		},
		tallyPaper{doi: "10.1/none", year: 2010, relevance: relevanceDirect, direction: directionNull},
	)
	// an old artifact: no stored field, the abstract names the number
	st.Enriched[1].Abstract = "Registered as NCT01234567."

	prompt, err := synthesisUserPrompt(st)
	require.NoError(t, err)

	var data struct {
		Studies []struct {
			DOI string   `json:"doi"`
			Reg []string `json:"trial_registrations"`
		} `json:"studies"`
	}

	require.NoError(t, json.Unmarshal([]byte(prompt[len("Build the evidence assessment for this dataset:\n\n"):]), &data))
	require.Len(t, data.Studies, 2)
	assert.Equal(t, []string{regReflux}, data.Studies[0].Reg)
	assert.Equal(t, []string{regNCT}, data.Studies[1].Reg)

	assert.Contains(t, synthesisSystemPrompt, "share a registration number")
	assert.Contains(t, RenderContext(st), fmt.Sprintf("trial registration: %s", regReflux))
}
