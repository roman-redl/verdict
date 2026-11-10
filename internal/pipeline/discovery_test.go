package pipeline

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// hit is a semantic-search result.
func hit(doi, title string) model.Paper {
	return model.Paper{DOI: doi, Title: title, StudyDesign: designRCT, Year: 2023, Source: model.SourceConsensus}
}

// pageOf builds a search page; end marks the end of the ranking.
func pageOf(end bool, papers ...model.Paper) model.SearchPage {
	return model.SearchPage{Papers: papers, IsEnd: end}
}

// Mode B walks pages until the ranking ends, recording the provenance.
func TestDiscoverConsensusWalksPagesToEnd(t *testing.T) {
	search := &fakeSearch{key: true, page: func(_ string, page int) (model.SearchPage, error) {
		if page == 0 {
			return pageOf(false, hit("10.1/a", "A"), hit("10.1/b", "B")), nil
		}

		return pageOf(true, hit("10.1/c", "C")), nil
	}}
	p := newTest(t, Settings{ConsensusPages: 3, MaxPapers: 50}, Clients{Search: search}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(context.Background(), st, Options{Question: "q"}))

	assert.Equal(t, 2, search.calls.total(), "walk to the end of the ranking")

	require.Len(t, st.Papers, 3)

	d := st.Discovery
	require.NotNil(t, d, "discovery info")
	assert.Equal(t, EngineConsensus, d.Engine, "discovery info")
	assert.Equal(t, 2, d.PagesFetched, "discovery info")
	assert.Equal(t, 3, d.PapersFound, "discovery info")
	assert.False(t, d.Truncated, "discovery info")

	require.Len(t, d.Queries, 1)
	assert.Equal(t, "q", d.Queries[0], "queries not recorded")

	for _, pp := range st.Papers {
		assert.Equal(t, model.SourceConsensus, pp.Source, "paper mapping")
		assert.Equal(t, designRCT, pp.StudyDesign, "paper mapping")
	}
}

// The page cap bounds the quota spend; hitting it is recorded as Truncated.
func TestDiscoverConsensusPagesCapTruncates(t *testing.T) {
	search := &fakeSearch{key: true, page: func(_ string, page int) (model.SearchPage, error) {
		return pageOf(false, hit("10.1/p"+strconv.Itoa(page), "P")), nil
	}}
	p := newTest(t, Settings{MaxPapers: 50}, Clients{Search: search}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(context.Background(), st, Options{Question: "q", ConsensusPages: 2}))

	assert.Equal(t, 2, search.calls.total(), "page cap")

	assert.True(t, st.Discovery.Truncated, "cap hit on a not-ended ranking must set Truncated")
}

// --max-papers cuts the corpus, not the search: PapersFound keeps the
// retrieved count and the pool keeps every candidate.
func TestDiscoverConsensusCutsTheCorpusAtMaxPapers(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		return pageOf(false, hit("10.1/a", "A"), hit("10.1/b", "B"), hit("10.1/c", "C")), nil
	}}
	p := newTest(t, Settings{MaxPapers: 50}, Clients{Search: search}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(context.Background(), st, Options{Question: "q", MaxPapers: 1}))

	assert.Equal(t, 1, search.calls.total())

	assert.Len(t, st.Papers, 1, "truncated")

	assert.Equal(t, 3, st.Discovery.PapersFound, "PapersFound keeps the retrieved count")
	assert.Len(t, st.Discovery.Pool, 3, "the pool keeps what the cut dropped")
}

// Multiple queries dedupe by DOI; DOI-less results are dropped with a
// counted warning (the pipeline keys papers by DOI).
func TestDiscoverConsensusDedupesAndDropsNoDOI(t *testing.T) {
	search := &fakeSearch{key: true, page: func(q string, _ int) (model.SearchPage, error) {
		if q == "second" {
			return pageOf(true, hit("10.1/b", "B"), hit("10.1/c", "C"), hit("", "No DOI book")), nil
		}

		return pageOf(true, hit("10.1/a", "A"), hit("10.1/b", "B")), nil
	}}
	p := newTest(t, Settings{MaxPapers: 50}, Clients{Search: search}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(context.Background(), st, Options{
		Question: "q", Queries: []string{"first", "second"},
	}))

	require.Len(t, st.Papers, 3, "A,B,C after dedup")

	assert.Equal(t, 1, st.Discovery.DroppedNoDOI)

	require.NotEmpty(t, st.Warnings, "no-DOI drop must warn")
	assert.Contains(t, st.Warnings[0], "without a DOI", "no-DOI drop must warn")
}

// With a key configured, a bare -q discovers through Consensus, not OpenAlex.
func TestDiscoverDefaultsToConsensusWhenKeyPresent(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		return pageOf(true, hit("10.1/a", "A")), nil
	}}
	works := &fakeWorks{}
	p := newTest(t, Settings{MaxPapers: 50}, Clients{Search: search, Works: works}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(context.Background(), st, Options{Question: "q"}))

	assert.Positive(t, search.calls.total(), "consensus must be queried")
	assert.Zero(t, works.calls.total(), "no openalex fallback")

	assert.Equal(t, EngineConsensus, st.Discovery.Engine)
}

// Without a key the bare -q path stays mode A (keyword OpenAlex) with a
// warning pointing at the semantic alternatives.
func TestDiscoverFallsBackToOpenAlexWithoutKey(t *testing.T) {
	search := &fakeSearch{key: false} // present but keyless
	works := &fakeWorks{}
	p := newTest(t, Settings{MaxPapers: 50}, Clients{Search: search, Works: works}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(context.Background(), st, Options{Question: "q"}))

	assert.Equal(t, 1, works.calls.get("Search"))
	assert.Zero(t, search.calls.total())

	require.NotNil(t, st.Discovery)
	assert.Equal(t, EngineOpenAlex, st.Discovery.Engine)
}

// The OpenAlex path asks for the configured number of papers.
func TestDiscoverOpenAlexPassesTheLimit(t *testing.T) {
	var gotQuery string

	var gotLimit int

	works := &fakeWorks{search: func(q string, limit int) ([]model.Paper, error) {
		gotQuery, gotLimit = q, limit

		return []model.Paper{{DOI: "10.1/a", Title: "A", Source: model.SourceOpenAlex}}, nil
	}}
	p := newTest(t, Settings{MaxPapers: 15}, Clients{Works: works}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(context.Background(), st, Options{Question: "does it work?", MaxPapers: 7}))

	assert.Equal(t, "does it work?", gotQuery)
	assert.Equal(t, 7, gotLimit)
	assert.Len(t, st.Papers, 1)
}

// --engine forces the keyword path even with a key; an unknown value errors.
func TestDiscoverEngineOverride(t *testing.T) {
	search := &fakeSearch{key: true}
	works := &fakeWorks{}
	p := newTest(t, Settings{MaxPapers: 50}, Clients{Search: search, Works: works}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(context.Background(), st, Options{Question: "q", Engine: EngineOpenAlex}))

	assert.Equal(t, 1, works.calls.get("Search"))
	assert.Zero(t, search.calls.total())

	err := p.discoverWith(context.Background(), &State{}, Options{Question: "q", Engine: "bogus"})
	require.Error(t, err, "unknown engine must error")

	err = p.discoverWith(context.Background(), &State{}, Options{Question: "q", Engine: EngineDOIs})
	require.Error(t, err, "the dois engine is not selectable by name")
}

// A Consensus failure must kill the stage loudly (never a silent keyword
// fallback) and point at the free mode C escape hatch.
func TestDiscoverConsensusErrorIsLoud(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		return model.SearchPage{}, errors.New("HTTP 401 from consensus: bad key")
	}}
	works := &fakeWorks{}
	p := newTest(t, Settings{MaxPapers: 50}, Clients{Search: search, Works: works}, quietLog())

	err := p.discoverWith(context.Background(), &State{}, Options{Question: "q"})
	require.Error(t, err, "consensus 401 must fail the stage")

	assert.Positive(t, search.calls.total(), "consensus was queried")
	assert.Zero(t, works.calls.total(), "no silent keyword fallback")

	require.ErrorContains(t, err, "mode C", "error must point at the mode C escape hatch")
	require.ErrorContains(t, err, "bad key", "the cause stays in the chain")
}

// The corpus cut takes the pool by rank across queries — the second
// reformulation is never crowded out by the first (regression: queries after
// the cap-filler never ran).
func TestDiscoverConsensusFairShareAcrossQueries(t *testing.T) {
	mkPage := func(prefix string) model.SearchPage {
		papers := make([]model.Paper, 20)
		for i := range papers {
			papers[i] = hit("10.1/"+prefix+strconv.Itoa(i), strings.ToUpper(prefix)+strconv.Itoa(i))
		}

		return pageOf(false, papers...)
	}

	search := &fakeSearch{key: true, page: func(q string, _ int) (model.SearchPage, error) {
		if strings.HasPrefix(q, "second") {
			return mkPage("s"), nil
		}

		return mkPage("f"), nil
	}}
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 10}, Clients{Search: search}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(context.Background(), st,
		Options{Question: "main", Queries: []string{"first q", "second q"}}))

	assert.Equal(t, 2, search.calls.total(), "page 0 of each query")

	require.Len(t, st.Papers, 10, "the cap")

	f, s := 0, 0

	for _, pp := range st.Papers {
		switch {
		case strings.HasPrefix(pp.DOI, "10.1/f"):
			f++
		case strings.HasPrefix(pp.DOI, "10.1/s"):
			s++
		}
	}

	assert.Equal(t, 5, f, "fair share: first-query papers")
	assert.Equal(t, 5, s, "fair share: second-query papers")

	assert.Equal(t, 40, st.Discovery.PapersFound, "PapersFound must keep the pooled dedup count")
}

// A run needs a question or a DOI list, and the metadata source it uses.
func TestDiscoverValidatesInput(t *testing.T) {
	p := newTest(t, Settings{MaxPapers: 5}, Clients{}, quietLog())

	var ue *UserError

	require.ErrorAs(t, p.discoverWith(context.Background(), &State{}, Options{}), &ue)
	assert.Contains(t, ue.Msg, "question")

	err := p.discoverWith(context.Background(), &State{}, Options{Question: "q"})
	require.ErrorIs(t, err, errNoWorks, "keyword discovery without a metadata source")

	err = p.discoverWith(context.Background(), &State{}, Options{Question: "q", Engine: EngineConsensus})
	require.ErrorAs(t, err, &ue, "semantic discovery without a configured engine")
}
