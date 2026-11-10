package pipeline

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// The whole mode B discovery: Consensus hits, the references of the review
// among them, the screening, the selection — trials first, then other primary
// studies, then the review.
func TestDiscoverConsensusMinesScreensAndSelects(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		review := hit(doiReview, "Creatine: a systematic review")
		review.StudyDesign = tagSystematicReview
		cohort := hit("10.1/cohort", "A cohort of creatine users")
		cohort.StudyDesign = tagCohort
		editorial := hit("10.1/editorial", "Creatine, an editorial")

		return pageOf(true, review, hit(doiPoolTrial, "A creatine trial"), cohort, editorial), nil
	}}
	works := reviewWorks(map[string][]string{doiReview: {"W1", "W2", "W3"}})
	works.byIDs = func([]string) ([]model.Work, error) {
		return []model.Work{
			{ID: "W1", DOI: "https://doi.org/10.5/old-trial", Title: "An old creatine trial", Year: 1999},
			{ID: "W2", DOI: "https://doi.org/10.1/trial", Title: "A creatine trial"}, // already a hit
			{ID: "W3", DOI: "https://doi.org/10.5/off-topic", Title: "Statins in rats"},
		}, nil
	}
	screen := &screenFake{fn: answerBy(t, func(it screenItem) (bool, string) {
		switch {
		case strings.Contains(it.Title, "editorial"), strings.Contains(it.Title, "rats"):
			return false, guessOther
		case strings.Contains(it.Title, "review"):
			return true, "review"
		case strings.Contains(it.Title, "cohort"):
			return true, "other_primary"
		}

		return true, guessRCT
	})}
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10, LLMConcurrency: 1},
		Clients{Search: search, Works: works, LLMPlan: screen}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{Question: "does creatine help?"}))

	assert.Equal(t, []string{doiPoolTrial, "10.5/old-trial", "10.1/cohort", doiReview}, dois(st.Papers))
	assert.Equal(t, model.SourceReviewRefs, st.Papers[1].Source)

	d := st.Discovery
	assert.Equal(t, 4, d.PapersFound, "the Consensus hits")
	assert.Equal(t, 1, d.ReviewsMined)
	assert.Equal(t, 2, d.RefsAdded, "the old trial and the off-topic paper; the third reference was a hit already")
	require.Len(t, d.Pool, 6)

	byDOI := map[string]Candidate{}
	for _, c := range d.Pool {
		byDOI[c.DOI] = c
	}

	trial := byDOI[doiPoolTrial]
	assert.Equal(t, 1, trial.citingReviews())
	assert.True(t, byDOI["10.5/old-trial"].Selected)
	assert.False(t, byDOI["10.5/off-topic"].Selected)
	assert.False(t, byDOI["10.5/off-topic"].Screen.Include)
	assert.False(t, byDOI["10.1/editorial"].Selected)
	assert.Equal(t, []FoundBy{{Source: model.SourceConsensus, Query: 1, Rank: 3}}, byDOI["10.1/cohort"].Sources,
		"the Consensus query and rank stay on the dropped candidates too")
	assert.Equal(t, []FoundBy{{Source: model.SourceConsensus, Query: 1, Rank: 4}}, byDOI["10.1/editorial"].Sources)

	assert.Equal(t, 1, screen.calls, "6 candidates, one batch")
	require.NotNil(t, st.Cost)
	assert.Equal(t, 1, st.Cost.Screening.Calls)
	assert.Positive(t, st.Cost.Screening.InputTokens)
	assert.Nil(t, st.Cost.Critique, "the screening has its own line in the cost record")
}

// A failing screening does not fail discovery: Consensus hits stay in, mined
// references stay out.
func TestDiscoverConsensusScreeningFailureKeepsHitsDropsReferences(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		review := hit(doiReview, "Creatine: a systematic review")
		review.StudyDesign = tagSystematicReview

		return pageOf(true, review, hit(doiPoolTrial, "A creatine trial")), nil
	}}
	works := reviewWorks(map[string][]string{doiReview: {"W1"}})
	screen := &screenFake{fn: func(int, string) (string, error) { return "", errors.New("exit status 1") }}
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10},
		Clients{Search: search, Works: works, LLMPlan: screen}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{Question: "q"}))

	assert.Equal(t, []string{doiPoolTrial, doiReview}, dois(st.Papers))
	assert.Equal(t, 1, st.Discovery.RefsAdded, "the reference is in the pool for the record")
	assert.Contains(t, strings.Join(st.Warnings, "\n"),
		"1 candidates found only by PubMed or review references were left out")
}

// Without a screening model the stage still runs: no mining, no screening,
// the hits are selected by design group and rank.
func TestDiscoverConsensusWithoutScreeningModel(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		review := hit(doiReview, "Creatine: a systematic review")
		review.StudyDesign = tagSystematicReview

		return pageOf(true, review, hit(doiPoolTrial, "A creatine trial")), nil
	}}
	works := reviewWorks(map[string][]string{doiReview: {"W1"}})
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10}, Clients{Search: search, Works: works}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{Question: "q"}))

	assert.Equal(t, []string{doiPoolTrial, doiReview}, dois(st.Papers), "the trial goes before the review")
	assert.Zero(t, works.calls.total(), "nothing mined")
	assert.Nil(t, st.Cost)
	assert.Len(t, st.Discovery.Pool, 2)
}

// If screening excludes everything, the corpus is empty and says why.
func TestDiscoverConsensusScreeningExcludesAll(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		return pageOf(true, hit("10.1/a", "A")), nil
	}}
	screen := &screenFake{fn: answerBy(t, func(screenItem) (bool, string) { return false, guessOther })}
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10}, Clients{Search: search, LLMPlan: screen}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{Question: "q"}))

	assert.Empty(t, st.Papers)
	assert.Contains(t, strings.Join(st.Warnings, "\n"), "screening excluded all 1 candidates")
}

// Mode C and --dois runs are the user's list: no screening, no mining.
func TestDiscoverByDOIsIsNotScreened(t *testing.T) {
	works := &fakeWorks{byDOI: func(doi string) (*model.Work, error) { return work(doi, "T"), nil }}
	screen := &screenFake{fn: func(int, string) (string, error) { return "", errors.New("must not be called") }}
	p := newTest(t, Settings{MaxPapers: 5}, Clients{Works: works, LLMPlan: screen}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{DOIFile: doiList(t, listDOIA, listDOIB)}))

	assert.Len(t, st.Papers, 2)
	assert.Zero(t, screen.calls)
	assert.Nil(t, st.Discovery.Pool)
}
