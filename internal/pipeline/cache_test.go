package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		"10.1000/cached": {
			CheckedAt: time.Now(), Abstract: cachedAbstract,
			OALink: "https://x/cached.pdf", Retracted: false,
		},
	}

	require.NoError(t, prewarmed.Save(cachePath))

	p := New(Settings{Concurrency: 4, EnrichCacheTTL: ttl30d}, Clients{
		OpenAlex: openalex.NewWithBase(oa.URL, ""),
		Crossref: crossref.NewWithBase(cr.URL, ""),
	}, quietLog())

	st := &State{Papers: []model.Paper{
		{DOI: "10.1000/cached", Source: sourceManual},
		{DOI: "10.1000/new", Source: sourceManual},
	}}
	opts := Options{RunDir: filepath.Join(tmp, "run-1")}

	require.NoError(t, p.enrich(context.Background(), params(opts), st))

	assert.Equal(t, cachedAbstract, st.Enriched[0].Abstract)

	assert.Equal(t, "https://x/cached.pdf", st.Enriched[0].OALink)

	assert.Equal(t, 1, oaHits, "cached DOI must not be re-queried")
	// the cache now also covers the fresh DOI
	saved := LoadEnrichCache(cachePath, ttl30d, quietLog())
	assert.Contains(t, saved, "10.1000/new", "fetched DOI not written back to the cache")
}

func TestLoadEnrichCacheDropsStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	seeded := EnrichCache{
		"10.1/fresh": {CheckedAt: time.Now().Add(-time.Hour)},
		"10.1/stale": {CheckedAt: time.Now().Add(-100 * 24 * time.Hour)},
	}

	require.NoError(t, seeded.Save(path))

	got := LoadEnrichCache(path, ttl30d, quietLog())
	assert.Contains(t, got, "10.1/fresh", "fresh entry dropped")

	assert.NotContains(t, got, "10.1/stale", "stale entry kept")

	assert.Empty(t, LoadEnrichCache(filepath.Join(t.TempDir(), "missing.json"), time.Hour, quietLog()),
		"missing cache file must yield an empty cache")
}
