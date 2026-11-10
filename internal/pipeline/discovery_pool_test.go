package pipeline

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

// logBuffer is a logger whose output a test can read.
func logBuffer() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer

	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// distinctPage is a page of n hits with DOIs unique to (prefix, page).
func distinctPage(prefix string, page, n int, end bool) model.SearchPage {
	papers := make([]model.Paper, n)
	for i := range papers {
		papers[i] = hit(fmt.Sprintf("10.1/%s-%d-%d", prefix, page, i), "T")
	}

	return pageOf(end, papers...)
}

func TestAPICalls(t *testing.T) {
	tests := []struct{ papers, want int }{
		{0, 0}, {1, 1}, {20, 1}, {100, 1}, {101, 2}, {250, 3}, {300, 3}, {301, 4},
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.papers), func(t *testing.T) {
			assert.Equal(t, tt.want, apiCalls(tt.papers))
		})
	}
}

// The walk does not stop when the pool can already fill the corpus cap: the
// pool is screened and cut later, so every page up to the page cap is read.
func TestWalkHasNoEarlyStop(t *testing.T) {
	search := &fakeSearch{key: true, page: func(q string, page int) (model.SearchPage, error) {
		return distinctPage(q, page, 20, false), nil
	}}
	p := newTest(t, Settings{ConsensusPages: 3, MaxPapers: 5}, Clients{Search: search}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{Question: "q", Queries: []string{"a", "b"}}))

	assert.Equal(t, 6, search.calls.total(), "3 pages of each of the 2 queries, the cap of 5 papers notwithstanding")

	d := st.Discovery
	assert.Equal(t, 6, d.PagesFetched)
	assert.Equal(t, 6, d.APICalls, "20 papers a page is one call a page")
	assert.Equal(t, 120, d.PapersFound)
	assert.Len(t, d.Pool, 120)
	assert.True(t, d.Truncated, "the page cap stopped queries whose ranking went on")
	assert.Len(t, st.Papers, 5, "the corpus is cut to the cap")
}

// The API calls follow the papers returned, not the pages fetched, and the
// applied page size is recorded; a plan that caps it below the request is
// visible at once.
func TestDiscoverConsensusRecordsAPICallsAndPageSize(t *testing.T) {
	search := &fakeSearch{key: true, page: func(_ string, page int) (model.SearchPage, error) {
		pg := distinctPage("big", page, 250, page == 1)
		pg.PageSize, pg.PageSizeAsked = 300, 300

		return pg, nil
	}}
	p := newTest(t, Settings{ConsensusPages: 5, MaxPapers: 5}, Clients{Search: search}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{Question: "q"}))

	d := st.Discovery
	assert.Equal(t, 2, d.PagesFetched)
	assert.Equal(t, 6, d.APICalls, "two pages of 250 papers: 3 calls each")
	assert.Equal(t, 300, d.PageSize)
	assert.Empty(t, st.Warnings, "a plan that applies the requested size is not warned about")
}

// The API ranks only the first 1000 papers: (page+1)*page_size <= 1000. The
// walk stays inside that window at the size the server applied.
func TestWalkStaysInsideTheRankingWindow(t *testing.T) {
	search := &fakeSearch{key: true, page: func(_ string, page int) (model.SearchPage, error) {
		pg := distinctPage("w", page, 5, false)
		pg.PageSize = 500

		return pg, nil
	}}
	p := newTest(t, Settings{ConsensusPages: 10, MaxPapers: 5}, Clients{Search: search}, quietLog())

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{Question: "q"}))

	assert.Equal(t, 2, search.calls.total(), "pages 0 and 1 of 500 fill the window; page 2 would be a 422")
	assert.True(t, st.Discovery.Truncated)
}

func TestDiscoverConsensusWarnsWhenThePlanCapsThePageSize(t *testing.T) {
	search := &fakeSearch{key: true, page: func(_ string, page int) (model.SearchPage, error) {
		pg := distinctPage("cap", page, 20, true)
		pg.PageSize, pg.PageSizeAsked = 20, 100

		return pg, nil
	}}
	log, buf := logBuffer()
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 5}, Clients{Search: search}, log)

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{Question: "q", Queries: []string{"a", "b"}}))

	assert.Equal(t, 20, st.Discovery.PageSize, "the applied size, not the requested one")
	assert.Equal(t, 1, bytes.Count(buf.Bytes(), []byte("applied a smaller page_size")), "warned once, not per page")
	assert.Contains(t, buf.String(), "requested=100")
	assert.Contains(t, buf.String(), "applied=20")
	assert.Contains(t, fmt.Sprint(st.Warnings), "page_size 20 applied instead of the requested 100")
}

// A Free key gets page 0 only: a 403 on a later page ends that query's walk
// with a warning, the pool keeps what was fetched, the run goes on.
func TestDiscoverConsensusPaginationDenied(t *testing.T) {
	search := &fakeSearch{key: true, page: func(q string, page int) (model.SearchPage, error) {
		if page > 0 {
			return model.SearchPage{}, fmt.Errorf("page %d: %w: HTTP 403", page, ErrPaginationDenied)
		}

		pg := distinctPage(q, page, 20, false)
		pg.PageSize = 20

		return pg, nil
	}}
	log, buf := logBuffer()
	p := newTest(t, Settings{ConsensusPages: 3, MaxPapers: 50}, Clients{Search: search}, log)

	st := &State{}
	require.NoError(t, p.discoverWith(t.Context(), st, Options{Question: "q", Queries: []string{"a", "b"}}))

	assert.Equal(t, 4, search.calls.total(), "page 0 and the refused page 1 of each query, no page 2")

	d := st.Discovery
	assert.Equal(t, 2, d.PagesFetched, "a refused page is not a fetched page")
	assert.Equal(t, 2, d.APICalls)
	assert.Equal(t, 40, d.PapersFound)
	assert.True(t, d.Truncated, "the ranking went on past what the plan lets through")
	assert.Equal(t, 2, bytes.Count(buf.Bytes(), []byte("pagination needs a paid Consensus plan")), "one warning per query")
	assert.Contains(t, buf.String(), "applied_page_size=20", "the warning names the applied page size")
	assert.Contains(t, fmt.Sprint(st.Warnings), "paid Consensus plan")
}

// A refusal on page 0 is a bad key or a spent quota, not a plan limit: the
// run fails (only pages after the first are forgiven).
func TestDiscoverConsensusPageZeroDenialFails(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		return model.SearchPage{}, ErrPaginationDenied
	}}
	p := newTest(t, Settings{ConsensusPages: 2, MaxPapers: 5}, Clients{Search: search}, quietLog())

	require.ErrorIs(t, p.discoverWith(t.Context(), &State{}, Options{Question: "q"}), ErrPaginationDenied)
}

// A paper found by two queries keeps its rank in each; DOI-less results still
// count as ranking positions.
func TestWalkRanksAcrossQueries(t *testing.T) {
	w := newConsensusWalk([]string{"query one", "query two"}, nil)
	w.add(0, []model.Paper{hit("", "no doi"), hit("10.1/a", "A"), hit("10.1/b", "B")})
	w.add(1, []model.Paper{hit("10.1/b", "B"), hit("10.1/c", "C")})

	require.Len(t, w.pool.list, 3)
	assert.Equal(t, 1, w.info.DroppedNoDOI)

	byDOI := map[string]Candidate{}
	for _, c := range w.pool.list {
		byDOI[c.DOI] = c
	}

	cons := func(query, rank int) FoundBy { return FoundBy{Source: model.SourceConsensus, Query: query, Rank: rank} }

	assert.Equal(t, []FoundBy{cons(1, 2)}, byDOI["10.1/a"].Sources, "the DOI-less result held rank 1")
	assert.Equal(t, []FoundBy{cons(1, 3), cons(2, 1)}, byDOI["10.1/b"].Sources, "its rank in each query")
	assert.Equal(t, []FoundBy{cons(2, 2)}, byDOI["10.1/c"].Sources)

	b := byDOI["10.1/b"]
	rank, query, ok := b.bestRank()
	assert.True(t, ok)
	assert.Equal(t, 1, rank, "rank 1 of the second query beats rank 3 of the first")
	assert.Equal(t, 2, query)
}

// The pool is saved with the artifact next to the kept papers, and an
// artifact written before the pool existed still loads on --resume.
func TestPoolIsPersistedAndOldArtifactsStillLoad(t *testing.T) {
	search := &fakeSearch{key: true, page: func(string, int) (model.SearchPage, error) {
		return pageOf(true, hit("10.1/a", "A"), hit("10.1/b", "B"), hit("10.1/c", "C")), nil
	}}
	p := newTest(t, Settings{ConsensusPages: 1, MaxPapers: 2}, Clients{Search: search}, quietLog())
	dir := t.TempDir()

	st, _, err := p.Run(t.Context(), Options{Question: "q", RunDir: dir, StopAfter: "1"})
	require.NoError(t, err)
	require.Len(t, st.Papers, 2)

	loaded, err := LoadState(dir)
	require.NoError(t, err)
	require.NotNil(t, loaded.Discovery)
	require.Len(t, loaded.Discovery.Pool, 3, "the whole pool, not only the kept papers")
	require.Len(t, loaded.Papers, 2)

	first := loaded.Discovery.Pool[0]
	assert.Equal(t, "10.1/a", first.DOI)
	assert.Equal(t, []FoundBy{{Source: model.SourceConsensus, Query: 1, Rank: 1}}, first.Sources)
	assert.Equal(t, designRCT, first.Design)
	assert.True(t, first.Selected)
	assert.False(t, loaded.Discovery.Pool[2].Selected, "the third hit fell to the cut")

	// the same directory loads as a resume: the stage is skipped, nothing is searched again
	before := search.calls.total()
	resumed, _, err := p.Run(t.Context(), Options{Question: "q", RunDir: dir, StopAfter: "1", Resume: true})
	require.NoError(t, err)
	assert.Equal(t, before, search.calls.total())
	assert.Equal(t, loaded.Discovery.Pool, resumed.Discovery.Pool)

	old := t.TempDir()
	oldJSON := `{"question":"q","discovery":{"engine":"consensus","queries":["q"],"pages_fetched":1,"papers_found":1},` +
		`"papers":[{"doi":"10.1/old","title":"Old","source":"consensus"}]}`
	require.NoError(t, os.WriteFile(filepath.Join(old, "1-discovery.json"), []byte(oldJSON), 0o600))

	got, _, err := p.Run(t.Context(), Options{Question: "q", RunDir: old, StopAfter: "1", Resume: true})
	require.NoError(t, err)
	require.Len(t, got.Papers, 1)
	assert.Empty(t, got.Discovery.Pool, "an old artifact has no pool")
	assert.Equal(t, 1, got.Discovery.PagesFetched)
}
