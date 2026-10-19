package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

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
	if err := p.discover(context.Background(), st, Options{Question: "q"}); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Errorf("HTTP hits: got %d, want 2 (walk to is_end)", hits)
	}
	if len(st.Papers) != 3 {
		t.Fatalf("papers: got %d, want 3", len(st.Papers))
	}
	d := st.Discovery
	if d == nil || d.Engine != "consensus" || d.PagesFetched != 2 || d.PapersFound != 3 || d.Truncated {
		t.Errorf("discovery info: got %+v", d)
	}
	if len(d.Queries) != 1 || d.Queries[0] != "q" {
		t.Errorf("queries not recorded: %v", d.Queries)
	}
	for _, pp := range st.Papers {
		if pp.Source != "consensus" || pp.StudyDesign != "rct" {
			t.Errorf("paper mapping: %+v", pp)
		}
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
	if err := p.discover(context.Background(), st, Options{Question: "q", ConsensusPages: 2}); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Errorf("HTTP hits: got %d, want 2 (page cap)", hits)
	}
	if !st.Discovery.Truncated {
		t.Errorf("cap hit on a not-ended ranking must set Truncated")
	}
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
	if err := p.discover(context.Background(), st, Options{Question: "q", MaxPapers: 1}); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("HTTP hits: got %d, want 1", hits)
	}
	if len(st.Papers) != 1 {
		t.Errorf("papers: got %d, want 1 (truncated)", len(st.Papers))
	}
	if st.Discovery.PapersFound != 3 {
		t.Errorf("PapersFound: got %d, want 3 (retrieved before the cut)", st.Discovery.PapersFound)
	}
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
	if err := p.discover(context.Background(), st, Options{
		Question: "q", Queries: []string{"first", "second"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(st.Papers) != 3 {
		t.Fatalf("papers: got %d, want 3 (A,B,C after dedup)", len(st.Papers))
	}
	if st.Discovery.DroppedNoDOI != 1 {
		t.Errorf("DroppedNoDOI: got %d, want 1", st.Discovery.DroppedNoDOI)
	}
	if len(st.Warnings) == 0 || !strings.Contains(st.Warnings[0], "without a DOI") {
		t.Errorf("no-DOI drop must warn, got %v", st.Warnings)
	}
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
	if err := p.discover(context.Background(), st, Options{Question: "q"}); err != nil {
		t.Fatal(err)
	}
	if csHits == 0 || oaHits != 0 {
		t.Errorf("hits: consensus=%d openalex=%d, want consensus>0 openalex=0", csHits, oaHits)
	}
	if st.Discovery.Engine != "consensus" {
		t.Errorf("engine: got %q, want consensus", st.Discovery.Engine)
	}
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
	if err := p.discover(context.Background(), st, Options{Question: "q"}); err != nil {
		t.Fatal(err)
	}
	if oaHits != 1 {
		t.Errorf("openalex hits: got %d, want 1", oaHits)
	}
	if st.Discovery == nil || st.Discovery.Engine != "openalex" {
		t.Errorf("engine: got %+v, want openalex", st.Discovery)
	}
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
	if err := p.discover(context.Background(), st, Options{Question: "q", Engine: "openalex"}); err != nil {
		t.Fatal(err)
	}
	if oaHits != 1 || csHits != 0 {
		t.Errorf("hits: openalex=%d consensus=%d, want 1/0", oaHits, csHits)
	}
	if err := p.discover(context.Background(), &State{}, Options{Question: "q", Engine: "bogus"}); err == nil {
		t.Error("unknown engine must error")
	}
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
	if err == nil {
		t.Fatal("consensus 401 must fail the stage")
	}
	if csHits == 0 || oaHits != 0 {
		t.Errorf("hits: consensus=%d openalex=%d, want consensus>0 openalex=0 (no fallback)", csHits, oaHits)
	}
	if !strings.Contains(err.Error(), "mode C") {
		t.Errorf("error must point at the mode C escape hatch, got: %v", err)
	}
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
	if err := p.discover(context.Background(), st, Options{Question: "main", Queries: []string{"first q", "second q"}}); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Errorf("HTTP hits: got %d, want 2 (page 0 of each query; deeper pages skipped)", hits)
	}
	if len(st.Papers) != 10 {
		t.Fatalf("papers: got %d, want 10 (the cap)", len(st.Papers))
	}
	f, s := 0, 0
	for _, pp := range st.Papers {
		switch {
		case strings.HasPrefix(pp.DOI, "10.1/f"):
			f++
		case strings.HasPrefix(pp.DOI, "10.1/s"):
			s++
		}
	}
	if f != 5 || s != 5 {
		t.Errorf("fair share: got %d first-query / %d second-query papers, want 5/5", f, s)
	}
	if st.Discovery.PapersFound != 40 {
		t.Errorf("PapersFound must keep the pooled dedup count: got %d, want 40", st.Discovery.PapersFound)
	}
}
