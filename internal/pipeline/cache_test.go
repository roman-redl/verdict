package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

func TestEnrichUsesTopicCache(t *testing.T) {
	works := &fakeWorks{byDOI: func(doi string) (*model.Work, error) {
		w := work(doi, "New paper")
		w.Abstract = "fresh abstract"

		return w, nil
	}}

	// A topic with a pre-warmed cache for 10.1000/cached.
	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "cache.json")
	prewarmed := EnrichCache{
		"10.1000/cached": {
			CheckedAt: testNow, Abstract: cachedAbstract,
			OALink: "https://x/cached.pdf", Retracted: false,
		},
	}

	require.NoError(t, prewarmed.Save(cachePath))

	p := newTest(t, Settings{Concurrency: 4, EnrichCacheTTL: ttl30d},
		Clients{Works: works, Notices: &fakeNotices{}}, quietLog())

	st := &State{Papers: []model.Paper{
		{DOI: "10.1000/cached", Source: sourceManual},
		{DOI: "10.1000/new", Source: sourceManual},
	}}
	opts := Options{RunDir: filepath.Join(tmp, "run-1")}

	require.NoError(t, p.enrich(context.Background(), params(opts), st))

	assert.Equal(t, cachedAbstract, st.Enriched[0].Abstract)

	assert.Equal(t, "https://x/cached.pdf", st.Enriched[0].OALink)

	assert.Zero(t, works.calls.get("ByDOI:10.1000/cached"), "cached DOI must not be re-queried")
	assert.Equal(t, 1, works.calls.get("ByDOI:10.1000/new"))
	// the cache now also covers the fresh DOI
	saved := LoadEnrichCache(cachePath, ttl30d, testNow, quietLog())
	assert.Contains(t, saved, "10.1000/new", "fetched DOI not written back to the cache")
}

// Retraction flags and notes survive a round trip through the cache.
func TestEnrichCacheCarriesFlags(t *testing.T) {
	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "cache.json")

	prewarmed := EnrichCache{
		doiRetr: {
			CheckedAt: testNow, Retracted: true, Reason: "Crossref/retraction-watch: retraction notice 10.1/n",
			Notes: []string{noteCorrection},
		},
	}

	require.NoError(t, prewarmed.Save(cachePath))

	p := newTest(t, Settings{Concurrency: 1, EnrichCacheTTL: ttl30d}, Clients{}, quietLog())
	st := &State{Papers: []model.Paper{{DOI: doiRetr, Source: sourceManual}}}

	require.NoError(t, p.enrich(context.Background(), params(Options{RunDir: filepath.Join(tmp, "run")}), st))

	e := st.Enriched[0]
	assert.True(t, e.Flags.Retracted)
	assert.Equal(t, "Crossref/retraction-watch: retraction notice 10.1/n", e.Flags.Reason)
	assert.Equal(t, []string{noteCorrection}, e.Flags.Notes)
}

func TestLoadEnrichCacheDropsStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	seeded := EnrichCache{
		"10.1/fresh": {CheckedAt: testNow.Add(-time.Hour)},
		"10.1/stale": {CheckedAt: testNow.Add(-100 * 24 * time.Hour)},
	}

	require.NoError(t, seeded.Save(path))

	got := LoadEnrichCache(path, ttl30d, testNow, quietLog())
	assert.Contains(t, got, "10.1/fresh", "fresh entry dropped")

	assert.NotContains(t, got, "10.1/stale", "stale entry kept")

	assert.Empty(t, LoadEnrichCache(filepath.Join(t.TempDir(), "missing.json"), time.Hour, testNow, quietLog()),
		"missing cache file must yield an empty cache")
}

// A disabled critique cache (TTL zero) loads nothing even from a full file.
func TestCritiqueCacheDisabledByZeroTTL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "critique-cache.json")
	seeded := CritiqueCache{"k": {CheckedAt: testNow}}

	require.NoError(t, seeded.Save(path))
	assert.Empty(t, LoadCritiqueCache(path, 0, testNow, quietLog()))
	assert.NotEmpty(t, LoadCritiqueCache(path, ttl30d, testNow, quietLog()))
}

func TestCachePaths(t *testing.T) {
	topic := Options{Topic: "Creatine!"}

	assert.Equal(t, filepath.Join("runs", "creatine", "cache.json"), enrichCachePath(topic))
	assert.Equal(t, filepath.Join("runs", "creatine", "critique-cache.json"), critiqueCachePath(topic))
	assert.Equal(t, filepath.Join("a", "cache.json"), enrichCachePath(Options{RunDir: filepath.Join("a", "run")}))
	assert.Empty(t, enrichCachePath(Options{}), "no topic and no run dir: nothing to share, no cache")
	assert.Empty(t, critiqueCachePath(Options{}))
}

// Saving an empty cache writes nothing; an unwritable location is an error.
func TestCacheSave(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, EnrichCache{}.Save(filepath.Join(dir, "empty.json")))
	assert.NoFileExists(t, filepath.Join(dir, "empty.json"))
	require.NoError(t, EnrichCache{"a": {}}.Save(""), "no path: nothing to do")
}
