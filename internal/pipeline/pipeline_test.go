package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Fixture values repeated across the shared tests (goconst): placeholder
// DOIs, corpus sources and the critique vocabulary. The relevance values
// reuse the package constants relevanceDirect and relevanceIndirect.
const (
	doiA              = "10.1/a"
	doiB              = "10.1/b"
	doiShared         = "10.1/shared"
	doiVoid           = "10.1/void"
	doiRetr           = "10.1/retr"
	qualityHigh       = "high"
	qualityModerate   = "moderate"
	qualityLow        = "low"
	relevanceTangent  = "tangential"
	directionPositive = "positive"
	directionNegative = "negative"
	directionMixed    = "mixed"
	directionNull     = "null"
	sourceManual      = model.SourceManual
	cachedAbstract    = "cached abstract"
	noteCorrection    = "correction notice 10.1/c"
	listDOIA          = "10.1000/a"
	listDOIB          = "10.1000/b"
	oaPDFA            = "https://oa.test/a.pdf"
	seed1             = "10.1000/seed1"
	seed2             = "10.1000/seed2"
)

// ttl30d is the default cache freshness window the tests run with.
const ttl30d = 30 * 24 * time.Hour

// testNow is the pinned clock of the tests.
var testNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// newTest builds a pipeline on the pinned clock.
func newTest(t *testing.T, set Settings, clients Clients, log *slog.Logger) *Pipeline {
	t.Helper()

	p, err := New(set, clients, log, func() time.Time { return testNow })
	require.NoError(t, err)

	return p
}

// params builds the run parameters a stage takes; the run directory is the
// options' RunDir.
func params(opts Options) runParams { return runParams{opts: opts, dir: opts.RunDir} }

// discoverWith runs the discovery stage with the given options.
func (p *Pipeline) discoverWith(ctx context.Context, st *State, opts Options) error {
	return p.discover(ctx, params(opts), st)
}

// Enrichment must not re-query OpenAlex for papers discovered through
// OpenAlex (everything ByDOI returns was already captured at discovery);
// manual stubs still get their lookup. Crossref flags retractions OpenAlex
// missed.
func TestEnrichSkipsDuplicateOpenAlexLookups(t *testing.T) {
	works := &fakeWorks{byDOI: func(doi string) (*model.Work, error) {
		switch doi {
		case doiVoid: // unknown to OpenAlex
			return nil, nil //nolint:nilnil // the port's "no data" answer
		case doiRetr:
			return work(doiRetr, "Later retracted"), nil
		}

		w := work(doi, "Study "+doi)
		w.Abstract = "abstract text"

		return w, nil
	}}
	notices := &fakeNotices{updates: func(doi string) ([]model.Notice, error) {
		if doi == doiRetr {
			return []model.Notice{{DOI: "10.1/notice", Type: "retraction", Label: "Retraction"}}, nil
		}

		return nil, nil
	}}
	citations := &fakeCitations{tallies: func([]string) (map[string]model.CitationTally, error) {
		return map[string]model.CitationTally{doiRetr: {Total: 10, Supporting: 1, Contradicting: 2}}, nil
	}}

	p := newTest(t, Settings{Concurrency: 4}, Clients{Citations: citations, Works: works, Notices: notices}, quietLog())

	st := &State{Papers: []model.Paper{
		{DOI: "10.1/oa", Source: model.SourceOpenAlex}, // discovery already queried it
		{DOI: "10.1/el", Source: sourceManual},         // never queried
		{DOI: doiVoid, Source: sourceManual},           // discovery lookup failed — one retry
		{DOI: doiRetr, Source: sourceManual},           // OpenAlex misses it, Crossref flags it
	}}
	require.NoError(t, p.enrich(context.Background(), params(Options{}), st))

	assert.Zero(t, works.calls.get("ByDOI:10.1/oa"), "openalex-sourced paper must not be re-queried")

	for _, d := range []string{"10.1/el", doiVoid, doiRetr} {
		assert.Equal(t, 1, works.calls.get("ByDOI:"+d), "%s: openalex lookups", d)
	}

	assert.Equal(t, "abstract text", st.Enriched[1].Abstract, "manual stub abstract backfill")

	retr := st.Enriched[3]
	assert.True(t, retr.Flags.Retracted, "crossref retraction not flagged")
	assert.Contains(t, retr.Flags.Reason, "Crossref", "crossref retraction not flagged")

	require.NotNil(t, retr.Tally, "scite tallies not attached")
	assert.Equal(t, 2, retr.Tally.Contradicting, "scite tallies not attached")
	assert.Equal(t, 1, citations.calls.get("Tallies"), "one batched tallies request")
}

// A DOI list above the default runs whole (user-curated), but the run gets a
// warning with the real LLM cost; an explicit --max-papers truncates.
func TestDiscoverByDOIsWarnsOnLargeList(t *testing.T) {
	p := newTest(t, Settings{MaxPapers: 3}, Clients{Works: &fakeWorks{}}, quietLog()) // every DOI unknown: all stubs

	f := filepath.Join(t.TempDir(), "dois.txt")
	list := []string{listDOIA, listDOIB, "10.1000/c", "10.1000/d", "10.1000/e"}

	require.NoError(t, os.WriteFile(f, []byte(strings.Join(list, "\n")), 0o600))

	st := &State{}
	require.NoError(t, p.discoverByDOIs(context.Background(), Options{DOIFile: f}, st))

	require.Len(t, st.Papers, 5, "user-curated list must run whole")

	require.NotEmpty(t, st.Warnings, "large list must warn about LLM cost")
	assert.Contains(t, st.Warnings[0], "LLM calls", "large list must warn about LLM cost")

	st = &State{}
	require.NoError(t, p.discoverByDOIs(context.Background(), Options{DOIFile: f, MaxPapers: 2}, st))

	require.Len(t, st.Papers, 2, "explicit cap")
}

// An unknown DOI and a failing lookup both leave a stub that keeps the DOI.
func TestDiscoverByDOIsKeepsStubs(t *testing.T) {
	works := &fakeWorks{byDOI: func(doi string) (*model.Work, error) {
		switch doi {
		case listDOIA:
			return work(doi, "Known"), nil
		case listDOIB:
			return nil, errors.New("openalex down")
		}

		return nil, nil //nolint:nilnil // the port's "no data" answer
	}}
	p := newTest(t, Settings{MaxPapers: 10, Concurrency: 2}, Clients{Works: works}, quietLog())

	f := filepath.Join(t.TempDir(), "dois.txt")
	require.NoError(t, os.WriteFile(f, []byte("10.1000/a\n10.1000/b\n10.1000/c\n"), 0o600))

	st := &State{}
	require.NoError(t, p.discoverByDOIs(context.Background(), Options{DOIFile: f}, st))

	require.Len(t, st.Papers, 3)
	assert.Equal(t, "Known", st.Papers[0].Title)
	assert.Equal(t, model.SourceOpenAlex, st.Papers[0].Source)

	for _, i := range []int{1, 2} {
		assert.Equal(t, sourceManual, st.Papers[i].Source, "stub for %s", st.Papers[i].DOI)
		assert.Empty(t, st.Papers[i].Title)
	}
}

func TestDiscoverByDOIsRejectsEmptyList(t *testing.T) {
	p := newTest(t, Settings{MaxPapers: 3}, Clients{Works: &fakeWorks{}}, quietLog())

	f := filepath.Join(t.TempDir(), "dois.txt")
	require.NoError(t, os.WriteFile(f, []byte("no identifiers in here\n"), 0o600))

	err := p.discoverByDOIs(context.Background(), Options{DOIFile: f}, &State{})

	var ue *UserError

	require.ErrorAs(t, err, &ue)
	assert.Contains(t, ue.Msg, "no DOIs")
}

// A cached enrichment must not skip the full-text pickup: a PDF pre-placed
// in the run directory is read whatever the cache knows, and no OA download
// happens outside full mode (regression: the cache-hit branch used to
// return before fetchFullText, silently disabling full texts).
func TestEnrichCacheHitStillPicksUpPDF(t *testing.T) {
	tmp := t.TempDir()

	runDir := filepath.Join(tmp, "run")
	require.NoError(t, os.MkdirAll(filepath.Join(runDir, "fulltext"), 0o750))

	pdf := filepath.Join(runDir, "fulltext", "10.1_cached.pdf")
	require.NoError(t, os.WriteFile(pdf, []byte("%PDF-1.4 stub"), 0o600))

	seeded := EnrichCache{"10.1/cached": {CheckedAt: testNow, Abstract: cachedAbstract}}
	require.NoError(t, seeded.Save(filepath.Join(tmp, "cache.json"))) // Dir(RunDir)/cache.json

	works := &fakeWorks{}
	pdfs := &fakePDFs{}
	p := newTest(t, Settings{Concurrency: 4, EnrichCacheTTL: ttl30d}, Clients{
		Citations: &fakeCitations{},
		Works:     works,
		PDFs:      pdfs,
		PDFText:   fakePDFText{text: "FULL TEXT OF THE CACHED STUDY"},
	}, quietLog())
	st := &State{Papers: []model.Paper{{DOI: "10.1/cached", Source: sourceManual}}}

	require.NoError(t, p.enrich(context.Background(), params(Options{RunDir: runDir}), st))

	assert.Zero(t, works.calls.total(), "cached paper must not re-query OpenAlex")
	assert.Zero(t, pdfs.calls.total(), "no download outside full mode")

	e := st.Enriched[0]
	assert.Equal(t, cachedAbstract, e.Abstract, "cached abstract lost")

	assert.Contains(t, e.FullText, "FULL TEXT OF THE CACHED STUDY", "pre-placed PDF not picked up on the cache-hit path")
}

// Semantic Scholar backfills what OpenAlex missed, but never overwrites it.
func TestEnrichBackfillsFromScholar(t *testing.T) {
	works := &fakeWorks{byDOI: func(doi string) (*model.Work, error) {
		w := work(doi, "T")
		w.Abstract = "openalex abstract"

		return w, nil
	}}
	scholar := &fakeScholar{lookup: func([]string) (map[string]model.ScholarRecord, error) {
		return map[string]model.ScholarRecord{
			doiA: {Abstract: "s2 abstract", TLDR: "short", OAPDF: "https://x/a.pdf", InfluentialCitations: 7},
			doiB: {Abstract: "s2 abstract B", OAPDF: "https://x/b.pdf"},
		}, nil
	}}
	p := newTest(t, Settings{Concurrency: 2}, Clients{Works: works, Scholar: scholar}, quietLog())

	st := &State{Papers: []model.Paper{
		{DOI: doiA, Source: sourceManual},
		{DOI: doiB, Source: sourceManual, Abstract: "own abstract"},
	}}
	require.NoError(t, p.enrich(context.Background(), params(Options{}), st))

	a, b := st.Enriched[0], st.Enriched[1]
	assert.Equal(t, "openalex abstract", a.Abstract, "S2 must not overwrite the OpenAlex abstract")
	assert.Equal(t, "short", a.TLDR)
	assert.Equal(t, 7, a.InfluentialCitations)
	assert.Equal(t, "https://x/a.pdf", a.OALink, "S2 supplies the missing OA link")
	assert.Equal(t, "own abstract", b.Abstract)
	assert.Equal(t, 1, scholar.calls.get("Lookup"), "one batched request for the whole corpus")
}

// The paid-scope steps run only with their keys, and only where they make
// sense: PubPeer for any paper, the Scite drill-down for contradicted ones.
func TestEnrichOptionalSources(t *testing.T) {
	tallies := map[string]model.CitationTally{doiA: {Contradicting: 2}, doiB: {Supporting: 5}}
	citations := &fakeCitations{
		key:     true,
		tallies: func([]string) (map[string]model.CitationTally, error) { return tallies, nil },
		contradicting: func(string) ([]string, error) {
			return []string{criticDOI}, nil
		},
	}
	commentary := &fakeCommentary{key: true, note: func(doi string) (string, error) {
		if doi == doiB {
			return "", errors.New("pubpeer down")
		}

		return "PubPeer commentary (2 comments): https://pubpeer.com/t/1", nil
	}}

	p := newTest(t, Settings{Concurrency: 2}, Clients{Citations: citations, Commentary: commentary}, quietLog())
	st := &State{Papers: []model.Paper{{DOI: doiA, Source: sourceManual}, {DOI: doiB, Source: sourceManual}}}

	require.NoError(t, p.enrich(context.Background(), params(Options{}), st))

	a, b := st.Enriched[0], st.Enriched[1]
	assert.Equal(t, []string{criticDOI}, a.ContradictingDOIs)
	assert.Contains(t, a.Flags.Notes, "PubPeer commentary (2 comments): https://pubpeer.com/t/1")
	assert.Empty(t, b.ContradictingDOIs, "no drill-down for an uncontradicted paper")
	assert.Zero(t, citations.calls.get("ContradictingSources:"+doiB))
	assert.Empty(t, b.Flags.Notes, "a failing PubPeer check adds nothing and does not fail the stage")

	// without the keys the same steps stay off
	citations2 := &fakeCitations{tallies: citations.tallies}
	commentary2 := &fakeCommentary{}
	p2 := newTest(t, Settings{Concurrency: 2}, Clients{Citations: citations2, Commentary: commentary2}, quietLog())
	st2 := &State{Papers: []model.Paper{{DOI: doiA, Source: sourceManual}}}

	require.NoError(t, p2.enrich(context.Background(), params(Options{}), st2))
	assert.Zero(t, citations2.calls.get("ContradictingSources:"+doiA))
	assert.Zero(t, commentary2.calls.total())
}

// Crossref notices become flags and notes; corrections never retract.
func TestApplyNotices(t *testing.T) {
	e := &model.EnrichedPaper{}
	applyNotices(e, []model.Notice{
		{DOI: "10.1/c", Type: "correction", Label: "Correction"},
		{DOI: "10.1/e", Type: "expression-of-concern", Label: "Expression of concern"},
		{DOI: "10.1/x", Type: "withdrawal"},
	})

	assert.False(t, e.Flags.Retracted)
	assert.Equal(t, []string{
		noteCorrection,
		"Expression of concern notice 10.1/e",
		"withdrawal notice 10.1/x",
	}, e.Flags.Notes)

	applyNotices(e, []model.Notice{{DOI: "10.1/r", Type: "retraction"}})
	assert.True(t, e.Flags.Retracted)
	assert.Equal(t, "Crossref/retraction-watch: retraction notice 10.1/r", e.Flags.Reason)
}

// A Scite outage costs the citation analytics, not the run.
func TestEnrichSurvivesScitOutage(t *testing.T) {
	citations := &fakeCitations{tallies: func([]string) (map[string]model.CitationTally, error) {
		return nil, errors.New("scite down")
	}}
	p := newTest(t, Settings{Concurrency: 2}, Clients{Citations: citations}, quietLog())
	st := &State{Papers: []model.Paper{{DOI: doiA, Source: sourceManual}}}

	require.NoError(t, p.enrich(context.Background(), params(Options{}), st))

	require.Len(t, st.Enriched, 1)
	assert.Nil(t, st.Enriched[0].Tally)
	require.NotEmpty(t, st.Warnings)
	assert.Contains(t, st.Warnings[0], "Scite unavailable")
}
