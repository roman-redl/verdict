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

// fakeTrials is the PubMed record lookup: canned accession numbers and
// publication types per DOI, or one error for every call.
type fakeTrials struct {
	calls   counter
	asked   [][]string // the DOIs of each call
	numbers map[string][]string
	types   map[string][]string
	err     error
}

var _ PubMedRecords = (*fakeTrials)(nil)

func (f *fakeTrials) Records(_ context.Context, dois []string) (map[string]model.PubMedDetail, error) {
	f.calls.add("Records")
	f.asked = append(f.asked, dois)

	if f.err != nil {
		return map[string]model.PubMedDetail{}, f.err
	}

	out := map[string]model.PubMedDetail{}

	for _, d := range dois {
		n, hasNumbers := f.numbers[d]
		t, hasTypes := f.types[d]

		if hasNumbers || hasTypes {
			out[d] = model.PubMedDetail{Registrations: n, PubTypes: t}
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
	// a report: its DOI and year, how its time point and outcome match the
	// question (empty: not stated) and its follow-up in weeks (0: not stated)
	rk := func(doi string, year int, tp model.TimepointMatch, oc model.OutcomeMatch, weeks float64) reportRank {
		ef := &model.Effect{TimepointMatch: tp, OutcomeMatch: oc}
		if weeks > 0 {
			ef.TimepointWeeks = &weeks
		}

		return rankOf(doi, year, ef)
	}

	const (
		yes, no, unclear = model.TimepointYes, model.TimepointNo, model.TimepointUnclear
		same, related    = model.OutcomeSame, model.OutcomeRelated
	)

	for _, tc := range []struct {
		name string
		a, b reportRank
		want bool
	}{
		{"the match beats the unclear time point", rk("a", 2008, yes, "", 52), rk("z", 2013, unclear, "", 260), true},
		{"the unclear one loses to the match", rk("z", 2013, unclear, "", 260), rk("a", 2008, yes, "", 52), false},
		{"an unstated time point reads as unclear", rk("a", 2008, yes, "", 0), rk("z", 2013, "", "", 0), true},
		{"no match loses to an unclear time point", rk("a", 2013, no, "", 0), rk("z", 2008, unclear, "", 0), false},
		{"the same time point match: the outcome decides", rk("a", 2008, yes, same, 0), rk("z", 2013, yes, related, 0), true},
		{"an unstated outcome match reads as the same", rk("z", 2008, yes, "", 0), rk("a", 2013, yes, related, 0), true},
		{"the same matches: the longer follow-up", rk("z", 2008, yes, same, 260), rk("a", 2013, yes, same, 52), true},
		{"a stated follow-up beats an unstated one", rk("z", 2008, yes, same, 52), rk("a", 2013, yes, same, 0), true},
		{"the same follow-up: the later year", rk("z", 2013, yes, same, 52), rk("a", 2008, yes, same, 52), true},
		{"the earlier year", rk("a", 2008, yes, same, 52), rk("z", 2013, yes, same, 52), false},
		{"an unknown year", rk("a", 0, yes, same, 52), rk("z", 2008, yes, same, 52), false},
		{"all equal, the lower DOI", rk("a", 2013, yes, same, 52), rk("b", 2013, yes, same, 52), true},
		{"all equal, the higher DOI", rk("b", 2013, yes, same, 52), rk("a", 2013, yes, same, 52), false},
		{"the same report", rk("a", 2013, yes, same, 52), rk("a", 2013, yes, same, 52), false},
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
func fillTestPipeline(t *testing.T, records PubMedRecords) (*Pipeline, *bytes.Buffer) {
	t.Helper()

	var logs bytes.Buffer

	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	return newTest(t, Settings{}, Clients{Records: records}, log), &logs
}

func TestFillRecordDetails(t *testing.T) {
	// a paper as OpenAlex typed it, so that only the registration numbers are open
	paper := func(doi, abstract string) model.EnrichedPaper {
		e := ep(doi)
		e.Abstract = abstract
		e.PublicationTypes = []string{pubArticle}

		return e
	}

	t.Run("the abstract names the number: PubMed is not asked", func(t *testing.T) {
		ft := &fakeTrials{numbers: map[string][]string{doiA: {"NCT99999999"}}}
		p, _ := fillTestPipeline(t, ft)
		enriched := []model.EnrichedPaper{paper(doiA, "Methods. Trial registration: ISRCTN15517081.")}

		p.fillRecordDetails(t.Context(), enriched)

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

		p.fillRecordDetails(t.Context(), enriched)

		assert.Equal(t, []string{regNCT}, enriched[0].TrialRegistrations)
		assert.Empty(t, enriched[1].TrialRegistrations)
		assert.Equal(t, []string{"NCT00000003"}, enriched[2].TrialRegistrations)
		assert.Empty(t, enriched[3].TrialRegistrations)
		require.Len(t, ft.asked, 1, "one batched lookup")
		assert.Equal(t, []string{doiA, doiB}, ft.asked[0])
		assert.Contains(t, logs.String(), "registrations_filled=1")
	})

	t.Run("PubMed types the papers no source typed, whatever their abstract says", func(t *testing.T) {
		ft := &fakeTrials{types: map[string][]string{doiA: {pubErratum}, doiB: {pubTrial}}}
		p, logs := fillTestPipeline(t, ft)
		untyped := paper(doiA, "Registered at NCT00000003.")
		untyped.PublicationTypes = nil
		typed := paper(doiB, "Registered at NCT00000004.")
		enriched := []model.EnrichedPaper{untyped, typed, paper(doiC, "No number.")}

		p.fillRecordDetails(t.Context(), enriched)

		assert.Equal(t, []string{pubErratum}, enriched[0].PublicationTypes)
		assert.Equal(t, []string{"NCT00000003"}, enriched[0].TrialRegistrations, "the abstract's number stays")
		assert.Equal(t, []string{pubArticle}, enriched[1].PublicationTypes, "a typed paper with a number is not asked")
		assert.Equal(t, [][]string{{doiA, doiC}}, ft.asked, "the untyped one, and the one with no number")
		assert.Contains(t, logs.String(), "typed=1")
		assert.NotEmpty(t, model.NonStudyKind(enriched[0].PublicationTypes))
	})

	t.Run("the types of every source are merged", func(t *testing.T) {
		ft := &fakeTrials{types: map[string][]string{doiA: {"Comment", "Journal Article"}}}
		p, _ := fillTestPipeline(t, ft)
		e := paper(doiA, "No number.")
		e.PublicationTypes = []string{pubArticle, "journal article"}
		enriched := []model.EnrichedPaper{e}

		p.fillRecordDetails(t.Context(), enriched)

		assert.Equal(t, []string{pubArticle, pubComment, "journal article"}, enriched[0].PublicationTypes)
	})

	t.Run("a PubMed failure only logs: the abstract numbers stay", func(t *testing.T) {
		ft := &fakeTrials{err: errors.New("pubmed is down")}
		p, logs := fillTestPipeline(t, ft)
		enriched := []model.EnrichedPaper{
			paper(doiA, "No number."),
			paper(doiB, regReflux),
		}

		p.fillRecordDetails(t.Context(), enriched)

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

		p.fillRecordDetails(t.Context(), enriched)

		assert.Empty(t, enriched[0].TrialRegistrations)
		assert.Zero(t, ft.calls.total())
	})

	t.Run("without the PubMed port the abstracts are enough", func(t *testing.T) {
		p, _ := fillTestPipeline(t, nil)
		enriched := []model.EnrichedPaper{paper(doiA, regReflux), paper(doiB, "none")}

		p.fillRecordDetails(t.Context(), enriched)

		assert.Equal(t, []string{regReflux}, enriched[0].TrialRegistrations)
		assert.Empty(t, enriched[1].TrialRegistrations)
	})
}

// The enrichment stage fills the field and the artifact carries it.
func TestEnrichStoresTrialRegistrations(t *testing.T) {
	ft := &fakeTrials{numbers: map[string][]string{doiB: {regReflux}}}
	p := newTest(t, Settings{Concurrency: 1}, Clients{Citations: &fakeCitations{}, Records: ft}, quietLog())

	st := &State{Papers: []model.Paper{
		{DOI: doiA, Abstract: "Trial registration: NCT01234567.", Source: sourceManual},
		{DOI: doiB, Abstract: "An abstract with no number.", Source: sourceManual},
	}}

	// the enrichment cache lands next to the run directory, so the run lives in a fresh one
	require.NoError(t, p.enrich(t.Context(), params(Options{RunDir: filepath.Join(t.TempDir(), "run")}), st))

	assert.Equal(t, []string{regNCT}, st.Enriched[0].TrialRegistrations)
	assert.Equal(t, []string{regReflux}, st.Enriched[1].TrialRegistrations)
	assert.Equal(t, [][]string{{doiA, doiB}}, ft.asked, "no source typed the papers, so PubMed is asked for both")

	b, err := json.Marshal(st)
	require.NoError(t, err)

	var back State
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, st.Enriched[1].TrialRegistrations, back.Enriched[1].TrialRegistrations, "the artifact round-trips")
}

// onePositive is the tally of a corpus that counts one positive study.
const onePositive = "1 positive"

// tallyPaper is one study of the tally tests.
type tallyPaper struct {
	doi       string
	year, n   int
	ids       []string
	relevance string
	direction string
	design    model.StudyDesign
	// effect carries the matches that rank the reports of one trial; pubTypes
	// are the paper's publication types
	effect    *model.Effect
	pubTypes  []string
	retracted bool
}

func tallyState(papers ...tallyPaper) *State {
	st := &State{}

	for i := range papers {
		tp := &papers[i]
		e := ep(tp.doi)
		e.Year, e.SampleSize, e.TrialRegistrations = tp.year, tp.n, tp.ids
		e.PublicationTypes, e.Flags.Retracted = tp.pubTypes, tp.retracted
		e.Abstract = "An abstract with no number."

		c := crit(tp.doi, qualityHigh, tp.relevance, tp.direction)
		c.StudyDesign, c.Effect = tp.design, tp.effect

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
			want: onePositive,
		},
		{
			name: "the same year: the lower DOI",
			papers: []tallyPaper{
				{doi: doiB, year: 2013, n: 900, ids: reg, relevance: relevanceDirect, direction: directionPositive},
				{doi: doiA, year: 2013, n: 100, ids: reg, relevance: relevanceDirect, direction: directionNull},
			},
			want: "1 null",
		},
		{
			name: "the report at the question's time point beats the later one",
			papers: []tallyPaper{
				{
					doi: doiOld, year: 2008, ids: reg, relevance: relevanceDirect, direction: directionPositive,
					effect: &model.Effect{TimepointMatch: model.TimepointYes},
				},
				{
					doi: doiNew, year: 2013, ids: reg, relevance: relevanceDirect, direction: directionNegative,
					effect: &model.Effect{TimepointMatch: model.TimepointNo},
				},
			},
			want: onePositive,
		},
		{
			name: "a paper that is not a study report is never counted",
			papers: []tallyPaper{
				{doi: doiA, year: 2008, relevance: relevanceDirect, direction: directionPositive},
				{
					doi: doiB, year: 2013, relevance: relevanceDirect, direction: directionNegative,
					pubTypes: []string{pubErratum},
				},
			},
			want: onePositive,
		},
		{
			name: "a letter that is also typed as a trial is a study report",
			papers: []tallyPaper{
				{
					doi: doiA, year: 2008, relevance: relevanceDirect, direction: directionPositive,
					pubTypes: []string{pubLetter, pubTrial},
				},
			},
			want: onePositive,
		},
		{
			name: "a retracted trial is never counted, as in the pool and the evidence base",
			papers: []tallyPaper{
				{doi: doiA, relevance: relevanceDirect, direction: directionPositive},
				{doi: doiB, relevance: relevanceDirect, direction: directionNegative, retracted: true},
				{doi: doiC, relevance: relevanceDirect, direction: directionPositive},
			},
			want: "2 positive",
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
			name: "a review sharing the number is not counted: primary studies only",
			papers: []tallyPaper{
				{doi: "10.1/trial", year: 2013, ids: reg, relevance: relevanceDirect, direction: directionPositive},
				{
					doi: "10.1/review", year: 2020, ids: reg, relevance: relevanceDirect, direction: directionNegative,
					design: model.DesignSystematicReview,
				},
				{
					doi: "10.1/meta", year: 2021, relevance: relevanceDirect, direction: directionNegative,
					design: model.DesignMetaAnalysis,
				},
			},
			want: onePositive,
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
