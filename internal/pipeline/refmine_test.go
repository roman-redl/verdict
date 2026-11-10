package pipeline

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// refWork is the OpenAlex record behind a reference ID: its DOI derives from
// the ID, "W7" is 10.5/w7.
func refWork(id string) model.Work {
	return model.Work{ID: id, DOI: "https://doi.org/10.5/" + strings.ToLower(id), Title: "Trial " + id, Year: 2020}
}

func refWorks(ids []string) []model.Work {
	out := make([]model.Work, len(ids))
	for i, id := range ids {
		out[i] = refWork(id)
	}

	return out
}

// reviewWorks is a fake OpenAlex where each review DOI has the given
// references (IDs) and every ID resolves to refWork.
func reviewWorks(refs map[string][]string) *fakeWorks {
	return &fakeWorks{
		byDOI: func(doi string) (*model.Work, error) {
			ids, ok := refs[doi]
			if !ok {
				return nil, nil
			}

			return &model.Work{ID: "W-" + doi, DOI: doi, References: ids}, nil
		},
		byIDs: func(ids []string) ([]model.Work, error) { return refWorks(ids), nil },
	}
}

func reviewCand(doi string, rank int) Candidate { return cons(doi, 1, rank, tagSystematicReview) }

func miningPipeline(t *testing.T, works *fakeWorks) *Pipeline {
	t.Helper()

	return newTest(t, Settings{}, Clients{Works: works, LLMPlan: &scriptCompleter{}}, quietLog())
}

func TestMineReviewRefsAddsCandidatesAndDedupes(t *testing.T) {
	works := reviewWorks(map[string][]string{
		"10.1/rev1": {"W1", "W2", "W2", "W3"}, // W2 listed twice: counts once
		"10.1/rev2": {"W2", "W4"},
	})
	works.byIDs = func(ids []string) ([]model.Work, error) {
		out := refWorks(ids)
		if len(ids) > 0 && ids[0] == "W1" {
			out = append(out, model.Work{ID: "W99", Title: "No DOI"}) // a reference without a DOI
		}

		return out, nil
	}
	known := cons("10.5/w4", 2, 3, designRCT) // already a Consensus hit
	info := &DiscoveryInfo{Pool: []Candidate{reviewCand("10.1/rev1", 1), reviewCand("10.1/rev2", 2), known}}

	miningPipeline(t, works).mineReviewRefs(t.Context(), info, false, nil)

	assert.Equal(t, 2, info.ReviewsMined)
	assert.Equal(t, 3, info.RefsAdded, "W1, W2 and W3; W4 was in the pool and W99 has no DOI")
	require.Len(t, info.Pool, 6)

	byDOI := map[string]Candidate{}
	for _, c := range info.Pool {
		byDOI[c.DOI] = c
	}

	refs := func(n int) FoundBy { return FoundBy{Source: model.SourceReviewRefs, CitingReviews: n} }

	assert.Equal(t, []FoundBy{refs(2)}, byDOI["10.5/w2"].Sources, "two reviews cite it; it has no rank")
	assert.Equal(t, []FoundBy{refs(1)}, byDOI["10.5/w1"].Sources)
	assert.Equal(t, "Trial W2", byDOI["10.5/w2"].Title, "the title comes from OpenAlex")

	assert.Equal(t, []FoundBy{{Source: model.SourceConsensus, Query: 2, Rank: 3}, refs(1)}, byDOI["10.5/w4"].Sources,
		"a hit already in the pool gains a citing review and keeps its rank")
	assert.Equal(t, model.SourceReviewRefs, byDOI["10.5/w2"].paper.Source)
}

func TestMineReviewRefsBoundsTheReviews(t *testing.T) {
	refs := map[string][]string{}

	var pool []Candidate

	for i := 1; i <= 12; i++ {
		doi := fmt.Sprintf("10.1/rev%d", i)
		refs[doi] = []string{fmt.Sprintf("W%d", i)}
		pool = append(pool, reviewCand(doi, 13-i)) // rev12 has the best rank
	}

	pool = append(pool, cons("10.1/cohort", 1, 1, tagCohort), cons("10.1/trial", 1, 2, designRCT))
	works := reviewWorks(refs)
	info := &DiscoveryInfo{Pool: pool}

	miningPipeline(t, works).mineReviewRefs(t.Context(), info, false, nil)

	assert.Equal(t, maxMinedReviews, info.ReviewsMined)

	for i := 12; i > 12-maxMinedReviews; i-- {
		assert.Equal(t, 1, works.calls.get(fmt.Sprintf("ByDOI:10.1/rev%d", i)), "rev%d is among the best ranked", i)
	}

	for i := 1; i <= 12-maxMinedReviews; i++ {
		assert.Zero(t, works.calls.get(fmt.Sprintf("ByDOI:10.1/rev%d", i)), "rev%d is beyond the bound", i)
	}

	assert.Zero(t, works.calls.get("ByDOI:10.1/cohort"), "a primary study is not mined")
	assert.Zero(t, works.calls.get("ByDOI:10.1/trial"))
}

func TestMineReviewRefsBoundsTheReferencesPerReview(t *testing.T) {
	ids := make([]string, 400)
	for i := range ids {
		ids[i] = fmt.Sprintf("W%d", i)
	}

	works := reviewWorks(map[string][]string{doiReview: ids})

	var asked []string

	works.byIDs = func(got []string) ([]model.Work, error) {
		asked = got
		return refWorks(got), nil
	}
	info := &DiscoveryInfo{Pool: []Candidate{reviewCand(doiReview, 1)}}

	miningPipeline(t, works).mineReviewRefs(t.Context(), info, false, nil)

	assert.Equal(t, ids[:maxRefsPerReview], asked, "the first references, no more")
	assert.Equal(t, maxRefsPerReview, info.RefsAdded)
}

// A review that fails to resolve is a warning: the other reviews still add.
func TestMineReviewRefsSurvivesAFailingReview(t *testing.T) {
	works := reviewWorks(map[string][]string{"10.1/rev2": {"W1"}, "10.1/rev3": {"W2"}})
	inner := works.byDOI
	works.byDOI = func(doi string) (*model.Work, error) {
		if doi == "10.1/rev1" {
			return nil, errors.New("HTTP 500 from openalex")
		}

		return inner(doi)
	}
	log, buf := logBuffer()
	p := newTest(t, Settings{}, Clients{Works: works, LLMPlan: &scriptCompleter{}}, log)
	info := &DiscoveryInfo{Pool: []Candidate{
		reviewCand("10.1/rev1", 1), reviewCand("10.1/rev2", 2), reviewCand("10.1/rev3", 3),
	}}

	p.mineReviewRefs(t.Context(), info, false, nil)

	assert.Equal(t, 2, info.ReviewsMined)
	assert.Equal(t, 2, info.RefsAdded)
	assert.Contains(t, buf.String(), "review reference mining failed")
	assert.Contains(t, buf.String(), "HTTP 500")

	// the references of a failing ByIDs are lost the same way
	works.byIDs = func([]string) ([]model.Work, error) { return nil, errors.New("HTTP 503 from openalex") }
	info = &DiscoveryInfo{Pool: []Candidate{reviewCand("10.1/rev2", 1)}}

	p.mineReviewRefs(t.Context(), info, false, nil)
	assert.Zero(t, info.ReviewsMined)
	assert.Zero(t, info.RefsAdded)
}

// Mining needs OpenAlex and the screening model: unscreened references would
// be hundreds of unjudged papers.
func TestMineReviewRefsNeedsWorksAndScreening(t *testing.T) {
	works := reviewWorks(map[string][]string{doiReview: {"W1"}})
	pool := func() *DiscoveryInfo { return &DiscoveryInfo{Pool: []Candidate{reviewCand(doiReview, 1)}} }

	info := pool()
	newTest(t, Settings{}, Clients{Works: works}, quietLog()).mineReviewRefs(t.Context(), info, false, nil)
	assert.Len(t, info.Pool, 1, "no screening model")

	// with the screening off on purpose the references are mined unjudged
	info = pool()
	newTest(t, Settings{}, Clients{Works: works}, quietLog()).mineReviewRefs(t.Context(), info, true, nil)
	assert.Len(t, info.Pool, 2, "--no-screen needs no screening model")
	assert.Equal(t, 1, works.calls.get("ByDOI:"+doiReview))

	info = pool()
	calls := works.calls.total()

	newTest(t, Settings{}, Clients{LLMPlan: &scriptCompleter{}}, quietLog()).mineReviewRefs(t.Context(), info, false, nil)
	assert.Len(t, info.Pool, 1, "no metadata source")
	assert.Equal(t, calls, works.calls.total())
}

func TestMergeReferencesKeepsTheMostCitedUpToTheBound(t *testing.T) {
	mk := func(prefix string, n int) reviewRefs {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = fmt.Sprintf("%s%d", prefix, i)
		}

		return reviewRefs{works: refWorks(ids), ok: true}
	}
	shared := refWork("Wshared")
	a, b, c := mk("Wa", 150), mk("Wb", 150), mk("Wc", 150)

	for _, r := range []*reviewRefs{&a, &b, &c} {
		r.works = append(r.works, shared)
	}

	pool, added := mergeReferences(nil, []reviewRefs{a, b, c}, nil)

	assert.Equal(t, maxMinedCandidates, added)
	require.Len(t, pool, maxMinedCandidates)
	assert.Equal(t, "10.5/wshared", pool[0].DOI, "the work cited by all three reviews comes first")
	assert.Equal(t, 3, pool[0].citingReviews())
	assert.Equal(t, "10.5/wa0", pool[1].DOI, "ties keep the order of the reviews, the best ranked first")
}
