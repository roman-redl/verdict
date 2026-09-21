package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/crossref"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/openalex"
)

func TestEnrichUsesTopicCache(t *testing.T) {
	var oaHits int
	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oaHits++
		_, _ = w.Write([]byte(`{"doi":"https://doi.org/10.1000/new","display_name":"New paper",
			"abstract_inverted_index":{"fresh":[0],"abstract":[1]}}`))
	}))
	defer oa.Close()
	cr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{}}`))
	}))
	defer cr.Close()

	// A topic with a pre-warmed cache for 10.1000/cached.
	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "cache.json")
	prewarmed := EnrichCache{
		"10.1000/cached": {CheckedAt: time.Now(), Abstract: "cached abstract",
			OALink: "https://x/cached.pdf", Retracted: false},
	}
	if err := prewarmed.Save(cachePath); err != nil {
		t.Fatal(err)
	}

	p := New(&config.Config{Concurrency: 4, EnrichCacheTTLDays: 30}, Clients{
		OpenAlex: openalex.NewWithBase(oa.URL, ""),
		Crossref: crossref.NewWithBase(cr.URL, ""),
	}, quietLog())

	st := &State{Papers: []model.Paper{
		{DOI: "10.1000/cached", Source: "manual"},
		{DOI: "10.1000/new", Source: "manual"},
	}}
	opts := Options{RunDir: filepath.Join(tmp, "run-1")}
	if err := p.enrich(context.Background(), st, opts); err != nil {
		t.Fatal(err)
	}

	if got := st.Enriched[0].Abstract; got != "cached abstract" {
		t.Errorf("cached abstract: got %q", got)
	}
	if st.Enriched[0].OALink != "https://x/cached.pdf" {
		t.Errorf("cached OA link: got %q", st.Enriched[0].OALink)
	}
	if oaHits != 1 {
		t.Errorf("openalex hits: %d (cached DOI must not be re-queried)", oaHits)
	}
	// the cache now also covers the fresh DOI
	saved := LoadEnrichCache(cachePath, 30*24*time.Hour)
	if _, ok := saved["10.1000/new"]; !ok {
		t.Errorf("fetched DOI not written back to the cache: %v", saved)
	}
}

func TestLoadEnrichCacheDropsStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	seeded := EnrichCache{
		"10.1/fresh": {CheckedAt: time.Now().Add(-time.Hour)},
		"10.1/stale": {CheckedAt: time.Now().Add(-100 * 24 * time.Hour)},
	}
	if err := seeded.Save(path); err != nil {
		t.Fatal(err)
	}
	got := LoadEnrichCache(path, 30*24*time.Hour)
	if _, ok := got["10.1/fresh"]; !ok {
		t.Error("fresh entry dropped")
	}
	if _, ok := got["10.1/stale"]; ok {
		t.Error("stale entry kept")
	}
	if got := LoadEnrichCache(filepath.Join(t.TempDir(), "missing.json"), time.Hour); len(got) != 0 {
		t.Error("missing cache file must yield an empty cache")
	}
}
