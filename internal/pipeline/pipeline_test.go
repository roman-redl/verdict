package pipeline

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/crossref"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/openalex"
	"github.com/roman-redl/verdict/internal/scite"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Повторяющиеся значения фикстур общих тестов (goconst): DOI-заглушки,
// источники корпусов и словарь критик. Словарные значения релевантности и
// движков переиспользуют константы пакета (relevanceDirect, engine*).
const (
	doiA              = "10.1/a"
	doiB              = "10.1/b"
	doiShared         = "10.1/shared"
	doiVoid           = "10.1/void"
	doiRetr           = "10.1/retr"
	qualityHigh       = "high"
	qualityModerate   = "moderate"
	qualityLow        = "low"
	relevanceIndirect = "indirect"
	directionPositive = "positive"
	directionNegative = "negative"
	directionMixed    = "mixed"
	directionNull     = "null"
	sourceManual      = "manual"
	modeLean          = "lean"
	cachedAbstract    = "cached abstract"
)

// ttl30d is the default cache freshness window the tests run with.
const ttl30d = 30 * 24 * time.Hour

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
		case doiVoid: // unknown to OpenAlex
			w.WriteHeader(http.StatusNotFound)
		case doiRetr:
			_, _ = w.Write([]byte(`{"doi":"https://doi.org/10.1/retr","display_name":"Later retracted"}`))
		default:
			//nolint:gosec // фейк-сервер теста: doi приходит из запроса к httptest, не пользовательский ввод
			_, _ = w.Write([]byte(`{"doi":"https://doi.org/` + doi + `","display_name":"Study ` + doi + `",
				"abstract_inverted_index":{"abstract":[0],"text":[1]}}`))
		}
	}))
	defer oa.Close()

	cr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, doiRetr) {
			_, _ = w.Write([]byte(`{"message":{"updated-by":[{"DOI":"10.1/notice","type":"retraction",` +
				`"label":"Retraction","source":"retraction-watch"}]}}`))

			return
		}

		_, _ = w.Write([]byte(`{"message":{}}`))
	}))
	defer cr.Close()

	sc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tallies":{"10.1/retr":{"total":10,"supporting":1,"contradicting":2}}}`))
	}))
	defer sc.Close()

	p := New(Settings{Concurrency: 4}, Clients{
		Scite:    scite.NewWithBase(sc.URL, ""),
		OpenAlex: openalex.NewWithBase(oa.URL, ""),
		Crossref: crossref.NewWithBase(cr.URL, ""),
	}, quietLog())

	st := &State{Papers: []model.Paper{
		{DOI: "10.1/oa", Source: engineOpenAlex}, // discovery already queried it
		{DOI: "10.1/el", Source: sourceManual},   // never queried
		{DOI: doiVoid, Source: sourceManual},     // discovery lookup failed — one retry
		{DOI: doiRetr, Source: sourceManual},     // OpenAlex misses it, Crossref flags it
	}}
	require.NoError(t, p.enrich(context.Background(), st, Options{}))

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, 0, oaCalls["10.1/oa"], "openalex-sourced paper must not be re-queried")

	for _, d := range []string{"10.1/el", doiVoid, doiRetr} {
		assert.Equal(t, 1, oaCalls[d], "%s: openalex lookups", d)
	}

	assert.Equal(t, "abstract text", st.Enriched[1].Abstract, "manual stub abstract backfill")

	retr := st.Enriched[3]
	assert.True(t, retr.Flags.Retracted, "crossref retraction not flagged")
	assert.Contains(t, retr.Flags.Reason, "Crossref", "crossref retraction not flagged")

	require.NotNil(t, retr.Tally, "scite tallies not attached")
	assert.Equal(t, 2, retr.Tally.Contradicting, "scite tallies not attached")
}

// A DOI list above the default runs whole (user-curated), but the run gets a
// warning with the real LLM cost; an explicit --max-papers truncates.
func TestDiscoverByDOIsWarnsOnLargeList(t *testing.T) {
	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // all stubs; enough for this test
	}))
	defer oa.Close()

	p := New(Settings{MaxPapers: 3}, Clients{
		OpenAlex: openalex.NewWithBase(oa.URL, ""),
	}, quietLog())

	dir := t.TempDir()
	f := dir + "/dois.txt"
	list := []string{"10.1000/a", "10.1000/b", "10.1000/c", "10.1000/d", "10.1000/e"}

	require.NoError(t, os.WriteFile(f, []byte(strings.Join(list, "\n")), 0o600))

	st := &State{}
	require.NoError(t, p.discoverByDOIs(context.Background(), st, Options{DOIFile: f}))

	require.Len(t, st.Papers, 5, "user-curated list must run whole")

	require.NotEmpty(t, st.Warnings, "large list must warn about LLM cost")
	assert.Contains(t, st.Warnings[0], "LLM calls", "large list must warn about LLM cost")

	st = &State{}
	require.NoError(t, p.discoverByDOIs(context.Background(), st, Options{DOIFile: f, MaxPapers: 2}))

	require.Len(t, st.Papers, 2, "explicit cap")
}

// A cached enrichment must not skip the full-text pickup: a PDF pre-placed
// in the run directory is read whatever the cache knows, and no OA download
// happens outside full mode (regression: the cache-hit branch used to
// return before fetchFullText, silently disabling full texts).
func TestEnrichCacheHitStillPicksUpPDF(t *testing.T) {
	restore := pdfTextExtractor
	pdfTextExtractor = func(b []byte, maxChars int) (string, error) {
		return "FULL TEXT OF THE CACHED STUDY", nil
	}

	defer func() { pdfTextExtractor = restore }()

	var oaHits atomic.Int32

	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oaHits.Add(1)

		_, _ = w.Write([]byte(`{"doi":"https://doi.org/10.1/cached"}`))
	}))
	defer oa.Close()

	sc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tallies":{}}`))
	}))
	defer sc.Close()

	tmp := t.TempDir()

	runDir := filepath.Join(tmp, "run")
	require.NoError(t, os.MkdirAll(filepath.Join(runDir, "fulltext"), 0o750))

	pdf := filepath.Join(runDir, "fulltext", "10.1_cached.pdf")
	require.NoError(t, os.WriteFile(pdf, []byte("%PDF-1.4 stub"), 0o600))

	seeded := EnrichCache{"10.1/cached": {CheckedAt: time.Now(), Abstract: cachedAbstract}}
	require.NoError(t, seeded.Save(filepath.Join(tmp, "cache.json"))) // Dir(RunDir)/cache.json

	p := New(Settings{Concurrency: 4, EnrichCacheTTL: ttl30d}, Clients{
		Scite:    scite.NewWithBase(sc.URL, ""),
		OpenAlex: openalex.NewWithBase(oa.URL, ""),
	}, quietLog())
	p.dir = runDir
	st := &State{Papers: []model.Paper{{DOI: "10.1/cached", Source: sourceManual}}}

	require.NoError(t, p.enrich(context.Background(), st, Options{RunDir: runDir}))

	assert.Equal(t, int32(0), oaHits.Load(), "cached paper must not re-query OpenAlex")

	e := st.Enriched[0]
	assert.Equal(t, cachedAbstract, e.Abstract, "cached abstract lost")

	assert.Contains(t, e.FullText, "FULL TEXT OF THE CACHED STUDY", "pre-placed PDF not picked up on the cache-hit path")
}
