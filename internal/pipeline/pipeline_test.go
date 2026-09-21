package pipeline

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/crossref"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/openalex"
	"github.com/roman-redl/verdict/internal/scite"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Enrichment must not re-query OpenAlex for papers discovered through
// OpenAlex (everything ByDOI returns was already captured at discovery);
// Manual stubs still get their lookup. Crossref flags
// retractions OpenAlex missed.
func TestEnrichSkipsDuplicateOpenAlexLookups(t *testing.T) {
	var mu sync.Mutex
	oaCalls := map[string]int{}
	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doi := strings.TrimPrefix(r.URL.Path, "/works/doi:")
		mu.Lock()
		oaCalls[doi]++
		mu.Unlock()
		switch doi {
		case "10.1/void": // unknown to OpenAlex
			w.WriteHeader(http.StatusNotFound)
		case "10.1/retr":
			_, _ = w.Write([]byte(`{"doi":"https://doi.org/10.1/retr","display_name":"Later retracted"}`))
		default:
			_, _ = w.Write([]byte(`{"doi":"https://doi.org/` + doi + `","display_name":"Study ` + doi + `",
				"abstract_inverted_index":{"abstract":[0],"text":[1]}}`))
		}
	}))
	defer oa.Close()
	cr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "10.1/retr") {
			_, _ = w.Write([]byte(`{"message":{"updated-by":[{"DOI":"10.1/notice","type":"retraction","label":"Retraction","source":"retraction-watch"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"message":{}}`))
	}))
	defer cr.Close()
	sc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tallies":{"10.1/retr":{"total":10,"supporting":1,"contradicting":2}}}`))
	}))
	defer sc.Close()

	p := New(&config.Config{Concurrency: 4}, Clients{
		Scite:    scite.NewWithBase(sc.URL, ""),
		OpenAlex: openalex.NewWithBase(oa.URL, ""),
		Crossref: crossref.NewWithBase(cr.URL, ""),
	}, quietLog())

	st := &State{Papers: []model.Paper{
		{DOI: "10.1/oa", Source: "openalex"}, // discovery already queried it
		{DOI: "10.1/el", Source: "manual"},   // never queried
		{DOI: "10.1/void", Source: "manual"}, // discovery lookup failed — one retry
		{DOI: "10.1/retr", Source: "manual"}, // OpenAlex misses it, Crossref flags it
	}}
	if err := p.enrich(context.Background(), st, Options{}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if n := oaCalls["10.1/oa"]; n != 0 {
		t.Errorf("openalex-sourced paper re-queried %d times, want 0", n)
	}
	for _, d := range []string{"10.1/el", "10.1/void", "10.1/retr"} {
		if n := oaCalls[d]; n != 1 {
			t.Errorf("%s: %d openalex lookups, want 1", d, n)
		}
	}
	if got := st.Enriched[1].Abstract; got != "abstract text" {
		t.Errorf("manual stub abstract backfill: got %q", got)
	}
	retr := st.Enriched[3]
	if !retr.Flags.Retracted || !strings.Contains(retr.Flags.Reason, "Crossref") {
		t.Errorf("crossref retraction not flagged: %+v", retr.Flags)
	}
	if retr.Tally == nil || retr.Tally.Contradicting != 2 {
		t.Errorf("scite tallies not attached: %+v", retr.Tally)
	}
}

// A DOI list above the default runs whole (user-curated), but the run gets a
// warning with the real LLM cost; an explicit --max-papers truncates.
func TestDiscoverByDOIsWarnsOnLargeList(t *testing.T) {
	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // all stubs; enough for this test
	}))
	defer oa.Close()
	p := New(&config.Config{MaxPapers: 3}, Clients{
		OpenAlex: openalex.NewWithBase(oa.URL, ""),
	}, quietLog())

	dir := t.TempDir()
	f := dir + "/dois.txt"
	list := []string{"10.1000/a", "10.1000/b", "10.1000/c", "10.1000/d", "10.1000/e"}
	if err := os.WriteFile(f, []byte(strings.Join(list, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &State{}
	if err := p.discoverByDOIs(context.Background(), st, Options{DOIFile: f}); err != nil {
		t.Fatal(err)
	}
	if len(st.Papers) != 5 {
		t.Fatalf("no cap: want all 5 papers, got %d", len(st.Papers))
	}
	if len(st.Warnings) == 0 || !strings.Contains(st.Warnings[0], "LLM calls") {
		t.Errorf("large list must warn about LLM cost, got %v", st.Warnings)
	}

	st = &State{}
	if err := p.discoverByDOIs(context.Background(), st, Options{DOIFile: f, MaxPapers: 2}); err != nil {
		t.Fatal(err)
	}
	if len(st.Papers) != 2 {
		t.Fatalf("explicit cap: want 2 papers, got %d", len(st.Papers))
	}
}
