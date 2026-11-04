package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/consensus"
	"github.com/roman-redl/verdict/internal/openalex"
)

// consensusServer fakes /v1/search: per (query, page) it serves the prepared
// envelope and counts requests.
func consensusServer(t *testing.T, hits *int32, resp func(query string, page int) string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)

		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		_, _ = w.Write([]byte(resp(q.Get("query"), page)))
	}))
}

func result(doi, title string) string {
	return `{"doi":"` + doi + `","title":"` + title + `","study_type":"rct","publish_year":2023}`
}

// Mode B walks pages until is_end, recording the provenance.
func TestDiscoverConsensusWalksPagesToEnd(t *testing.T) {
	var hits int32

	srv := consensusServer(t, &hits, func(q string, page int) string {
		switch page {
		case 0:
			return `{"results":[` + result("10.1/a", "A") + `,` + result("10.1/b", "B") + `],
				"page_size":20,"is_end":false,"next_page":1}`
		default:
			return `{"results":[` + result("10.1/c", "C") + `],"page_size":20,"is_end":true}`
		}
	})
	defer srv.Close()

	p := New(&config.Config{ConsensusPages: 3, MaxPapers: 50}, Clients{
		Consensus: consensus.NewWithBase(srv.URL, "k"),
	}, quietLog())

	st := &State{}
	require.NoError(t, p.discover(context.Background(), st, Options{Question: "q"}))

	assert.Equal(t, int32(2), hits, "walk to is_end")

	require.Len(t, st.Papers, 3)

	d := st.Discovery
	require.NotNil(t, d, "discovery info")
	assert.Equal(t, engineConsensus, d.Engine, "discovery info")
	assert.Equal(t, 2, d.PagesFetched, "discovery info")
	assert.Equal(t, 3, d.PapersFound, "discovery info")
	assert.False(t, d.Truncated, "discovery info")

	require.Len(t, d.Queries, 1)
	assert.Equal(t, "q", d.Queries[0], "queries not recorded")

	for _, pp := range st.Papers {
		assert.Equal(t, engineConsensus, pp.Source, "paper mapping")
		assert.Equal(t, "rct", pp.StudyDesign, "paper mapping")
	}
}

// The page cap bounds the quota spend; hitting it is recorded as Truncated.
func TestDiscoverConsensusPagesCapTruncates(t *testing.T) {
	var hits int32

	srv := consensusServer(t, &hits, func(q string, page int) string {
		return `{"results":[` + result("10.1/x", "X") + `],"page_size":20,"is_end":false,"next_page":` +
			strconv.Itoa(page+1) + `}`
	})
	defer srv.Close()

	p := New(&config.Config{MaxPapers: 50}, Clients{
		Consensus: consensus.NewWithBase(srv.URL, "k"),
	}, quietLog())

	st := &State{}
	require.NoError(t, p.discover(context.Background(), st, Options{Question: "q", ConsensusPages: 2}))

	assert.Equal(t, int32(2), hits, "page cap")

	assert.True(t, st.Discovery.Truncated, "cap hit on a not-ended ranking must set Truncated")
}

// An explicit --max-papers truncates the corpus after the page it crossed
// the cap on (one hit, one kept paper, PapersFound keeps the retrieved count).
func TestDiscoverConsensusStopsAtMaxPapers(t *testing.T) {
	var hits int32

	srv := consensusServer(t, &hits, func(q string, page int) string {
		return `{"results":[` + result("10.1/a", "A") + `,` + result("10.1/b", "B") + `,` +
			result("10.1/c", "C") + `],"page_size":20,"is_end":false}`
	})
	defer srv.Close()

	p := New(&config.Config{MaxPapers: 50}, Clients{
		Consensus: consensus.NewWithBase(srv.URL, "k"),
	}, quietLog())

	st := &State{}
	require.NoError(t, p.discover(context.Background(), st, Options{Question: "q", MaxPapers: 1}))

	assert.Equal(t, int32(1), hits)

	assert.Len(t, st.Papers, 1, "truncated")

	assert.Equal(t, 3, st.Discovery.PapersFound, "PapersFound keeps the retrieved count")
}

// Multiple queries dedupe by DOI; DOI-less results are dropped with a
// counted warning (the pipeline keys papers by DOI).
func TestDiscoverConsensusDedupesAndDropsNoDOI(t *testing.T) {
	var hits int32

	srv := consensusServer(t, &hits, func(q string, page int) string {
		if q == "second" {
			return `{"results":[` + result("10.1/b", "B") + `,` + result("10.1/c", "C") + `,
				{"doi":"","title":"No DOI book"}],"page_size":20,"is_end":true}`
		}

		return `{"results":[` + result("10.1/a", "A") + `,` + result("10.1/b", "B") + `],
			"page_size":20,"is_end":true}`
	})
	defer srv.Close()

	p := New(&config.Config{MaxPapers: 50}, Clients{
		Consensus: consensus.NewWithBase(srv.URL, "k"),
	}, quietLog())

	st := &State{}
	require.NoError(t, p.discover(context.Background(), st, Options{
		Question: "q", Queries: []string{"first", "second"},
	}))

	require.Len(t, st.Papers, 3, "A,B,C after dedup")

	assert.Equal(t, 1, st.Discovery.DroppedNoDOI)

	require.NotEmpty(t, st.Warnings, "no-DOI drop must warn")
	assert.Contains(t, st.Warnings[0], "without a DOI", "no-DOI drop must warn")
}

// With a key configured, a bare -q discovers through Consensus, not OpenAlex.
func TestDiscoverDefaultsToConsensusWhenKeyPresent(t *testing.T) {
	var csHits, oaHits int32

	cs := consensusServer(t, &csHits, func(q string, page int) string {
		return `{"results":[` + result("10.1/a", "A") + `],"page_size":20,"is_end":true}`
	})
	defer cs.Close()

	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&oaHits, 1)

		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer oa.Close()

	p := New(&config.Config{MaxPapers: 50}, Clients{
		Consensus: consensus.NewWithBase(cs.URL, "k"),
		OpenAlex:  openalex.NewWithBase(oa.URL, ""),
	}, quietLog())

	st := &State{}
	require.NoError(t, p.discover(context.Background(), st, Options{Question: "q"}))

	assert.Positive(t, csHits, "consensus must be queried")
	assert.Zero(t, oaHits, "no openalex fallback")

	assert.Equal(t, engineConsensus, st.Discovery.Engine)
}

// Without a key the bare -q path stays mode A (keyword OpenAlex) with a
// warning pointing at the semantic alternatives.
func TestDiscoverFallsBackToOpenAlexWithoutKey(t *testing.T) {
	var oaHits int32

	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&oaHits, 1)

		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer oa.Close()

	p := New(&config.Config{MaxPapers: 50}, Clients{
		Consensus: consensus.NewWithBase("http://127.0.0.1:1", ""), // present but keyless
		OpenAlex:  openalex.NewWithBase(oa.URL, ""),
	}, quietLog())

	st := &State{}
	require.NoError(t, p.discover(context.Background(), st, Options{Question: "q"}))

	assert.Equal(t, int32(1), oaHits)

	require.NotNil(t, st.Discovery)
	assert.Equal(t, engineOpenAlex, st.Discovery.Engine)
}

// --engine forces the keyword path even with a key; an unknown value errors.
func TestDiscoverEngineOverride(t *testing.T) {
	var csHits, oaHits int32

	cs := consensusServer(t, &csHits, func(q string, page int) string {
		return `{"results":[],"page_size":20,"is_end":true}`
	})
	defer cs.Close()

	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&oaHits, 1)

		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer oa.Close()

	p := New(&config.Config{MaxPapers: 50}, Clients{
		Consensus: consensus.NewWithBase(cs.URL, "k"),
		OpenAlex:  openalex.NewWithBase(oa.URL, ""),
	}, quietLog())

	st := &State{}
	require.NoError(t, p.discover(context.Background(), st, Options{Question: "q", Engine: engineOpenAlex}))

	assert.Equal(t, int32(1), oaHits)
	assert.Zero(t, csHits)

	err := p.discover(context.Background(), &State{}, Options{Question: "q", Engine: "bogus"})
	assert.Error(t, err, "unknown engine must error")
}

// A Consensus failure must kill the stage loudly (never a silent keyword
// fallback) and point at the free mode C escape hatch.
func TestDiscoverConsensusErrorIsLoud(t *testing.T) {
	var csHits, oaHits int32

	cs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&csHits, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":[{"msg":"bad key"}]}`))
	}))
	defer cs.Close()

	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&oaHits, 1)

		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer oa.Close()

	p := New(&config.Config{MaxPapers: 50}, Clients{
		Consensus: consensus.NewWithBase(cs.URL, "k"),
		OpenAlex:  openalex.NewWithBase(oa.URL, ""),
	}, quietLog())

	err := p.discover(context.Background(), &State{}, Options{Question: "q"})
	require.Error(t, err, "consensus 401 must fail the stage")

	assert.Positive(t, csHits, "consensus was queried")
	assert.Zero(t, oaHits, "no silent keyword fallback")

	assert.ErrorContains(t, err, "mode C", "error must point at the mode C escape hatch")
}

// Page 0 of every query is fetched before page 1 of any, and the corpus cap
// trims the pooled lists fairly — the second reformulation is never crowded
// out by the first (regression: queries after the cap-filler never ran).
func TestDiscoverConsensusFairShareAcrossQueries(t *testing.T) {
	mkResults := func(prefix string) string {
		parts := make([]string, 20)
		for i := range parts {
			parts[i] = result("10.1/"+prefix+strconv.Itoa(i), strings.ToUpper(prefix)+strconv.Itoa(i))
		}

		return `{"results":[` + strings.Join(parts, ",") + `],"page_size":20,"is_end":false}`
	}

	var hits int32

	srv := consensusServer(t, &hits, func(q string, page int) string {
		if strings.HasPrefix(q, "second") {
			return mkResults("s")
		}

		return mkResults("f")
	})
	defer srv.Close()

	p := New(&config.Config{ConsensusPages: 3, MaxPapers: 10}, Clients{
		Consensus: consensus.NewWithBase(srv.URL, "k"),
	}, quietLog())

	st := &State{}
	require.NoError(t, p.discover(context.Background(), st,
		Options{Question: "main", Queries: []string{"first q", "second q"}}))

	assert.Equal(t, int32(2), hits, "page 0 of each query; deeper pages skipped")

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
