package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	doiEmpty   = "10.1/empty"
	doiHas     = "10.1/has"
	epmcText   = "Objectives\nFrom Europe PMC."
	srcEuroPMC = "europepmc"
)

// abstractsFrom is a fake answer: every requested DOI that has an entry in
// known gets it.
func abstractsFrom(known map[string]string) func([]string) (map[string]string, error) {
	return func(dois []string) (map[string]string, error) {
		out := map[string]string{}

		for _, d := range dois {
			if text, ok := known[d]; ok {
				out[d] = text
			}
		}

		return out, nil
	}
}

func TestEnrichFillsOnlyMissingAbstracts(t *testing.T) {
	fb := &fakeAbstracts{abstracts: abstractsFrom(map[string]string{doiEmpty: epmcText, doiHas: "must not be used"})}
	works := &fakeWorks{byDOI: func(doi string) (*model.Work, error) {
		w := work(doi, "t")
		if doi == doiHas {
			w.Abstract = "from openalex"
		}

		return w, nil
	}}

	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "cache.json")
	p := newTest(t, Settings{Concurrency: 2, EnrichCacheTTL: ttl30d},
		Clients{Citations: &fakeCitations{}, Works: works, Abstracts: fb}, quietLog())

	st := &State{Papers: []model.Paper{
		{DOI: doiHas, Source: sourceManual},
		{DOI: "https://doi.org/" + doiEmpty, Source: sourceManual}, // asked for in normalized form
		{DOI: "10.1/unknown", Source: sourceManual},                // Europe PMC knows nothing
		{Title: "no DOI", Source: sourceManual},                    // cannot be looked up
	}}

	require.NoError(t, p.enrich(context.Background(), params(Options{RunDir: filepath.Join(tmp, "run")}), st))

	require.Len(t, fb.asked, 1, "one batched request")
	assert.ElementsMatch(t, []string{doiEmpty, "10.1/unknown"}, fb.asked[0])

	assert.Equal(t, "from openalex", st.Enriched[0].Abstract)
	assert.Empty(t, st.Enriched[0].AbstractSource, "a primary-source abstract carries no fallback mark")
	assert.Equal(t, epmcText, st.Enriched[1].Abstract)
	assert.Equal(t, srcEuroPMC, st.Enriched[1].AbstractSource)
	assert.Empty(t, st.Enriched[2].Abstract)
	assert.Empty(t, st.Enriched[3].Abstract)
	assert.Empty(t, st.Warnings)

	saved := LoadEnrichCache(cachePath, ttl30d, testNow, quietLog())
	assert.Equal(t, epmcText, saved[doiEmpty].Abstract, "the filled abstract is cached")
	assert.Equal(t, srcEuroPMC, saved[doiEmpty].AbstractSource)
	assert.Equal(t, "from openalex", saved[doiHas].Abstract)
	assert.Empty(t, saved[doiHas].AbstractSource)
}

// A cached entry without an abstract gets the fallback too (the cache was
// written before the fallback existed), and the next run reads it from the
// cache without asking again.
func TestEnrichFillsCachedEntryWithoutAbstract(t *testing.T) {
	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "cache.json")

	require.NoError(t, EnrichCache{
		doiEmpty: {CheckedAt: testNow, OALink: "https://x/oa.pdf"},
		doiHas:   {CheckedAt: testNow, Abstract: cachedAbstract},
	}.Save(cachePath))

	fb := &fakeAbstracts{abstracts: abstractsFrom(map[string]string{doiEmpty: epmcText})}
	works := &fakeWorks{}
	p := newTest(t, Settings{Concurrency: 2, EnrichCacheTTL: ttl30d},
		Clients{Citations: &fakeCitations{}, Works: works, Abstracts: fb}, quietLog())
	papers := []model.Paper{{DOI: doiEmpty, Source: sourceManual}, {DOI: doiHas, Source: sourceManual}}

	first := &State{Papers: papers}
	require.NoError(t, p.enrich(context.Background(), params(Options{RunDir: filepath.Join(tmp, "run-1")}), first))

	assert.Equal(t, [][]string{{doiEmpty}}, fb.asked)
	assert.Equal(t, epmcText, first.Enriched[0].Abstract)
	assert.Equal(t, "https://x/oa.pdf", first.Enriched[0].OALink, "the rest of the cached entry is kept")
	assert.Equal(t, cachedAbstract, first.Enriched[1].Abstract)
	assert.Zero(t, works.calls.total(), "cached DOIs are not re-queried")

	second := &State{Papers: papers}
	require.NoError(t, p.enrich(context.Background(), params(Options{RunDir: filepath.Join(tmp, "run-2")}), second))

	assert.Equal(t, 1, fb.calls.get("Abstracts"), "the filled abstract now comes from the cache")
	assert.Equal(t, epmcText, second.Enriched[0].Abstract)
	assert.Equal(t, srcEuroPMC, second.Enriched[0].AbstractSource, "the source mark survives the cache")
}

func TestEnrichFallbackFailureIsAWarning(t *testing.T) {
	fb := &fakeAbstracts{abstracts: func([]string) (map[string]string, error) {
		return map[string]string{doiA: "partial"}, errors.New("HTTP 503 from europepmc")
	}}

	tmp := t.TempDir()
	p := newTest(t, Settings{Concurrency: 2, EnrichCacheTTL: ttl30d},
		Clients{Citations: &fakeCitations{}, Abstracts: fb}, quietLog())
	st := &State{Papers: []model.Paper{{DOI: doiA, Source: sourceManual}, {DOI: doiB, Source: sourceManual}}}

	require.NoError(t, p.enrich(context.Background(), params(Options{RunDir: filepath.Join(tmp, "run")}), st))

	require.Len(t, st.Enriched, 2, "the stage goes on")
	assert.Equal(t, "partial", st.Enriched[0].Abstract, "what arrived before the failure is kept")
	assert.Empty(t, st.Enriched[1].Abstract)
	require.Len(t, st.Warnings, 1)
	assert.Contains(t, st.Warnings[0], "Europe PMC unavailable")
	assert.Contains(t, st.Warnings[0], "HTTP 503")
}

func TestEnrichFallbackNotCalledWhenNothingIsMissing(t *testing.T) {
	fb := &fakeAbstracts{}
	works := &fakeWorks{byDOI: func(doi string) (*model.Work, error) {
		w := work(doi, "t")
		w.Abstract = "present"

		return w, nil
	}}

	tmp := t.TempDir()
	p := newTest(t, Settings{Concurrency: 2, EnrichCacheTTL: ttl30d},
		Clients{Citations: &fakeCitations{}, Works: works, Abstracts: fb}, quietLog())
	st := &State{Papers: []model.Paper{{DOI: doiA, Source: sourceManual}, {Title: "no DOI", Abstract: "x"}}}

	require.NoError(t, p.enrich(context.Background(), params(Options{RunDir: filepath.Join(tmp, "run")}), st))

	assert.Zero(t, fb.calls.total())
	assert.Empty(t, st.Warnings)
}

// A resumed run whose enrichment artifact predates the fallback gets it, and
// the artifact and the cache are rewritten.
func TestResumeFillsLoadedEnrichment(t *testing.T) {
	tmp := t.TempDir()
	runDir := filepath.Join(tmp, "run")

	require.NoError(t, EnrichCache{doiEmpty: {CheckedAt: testNow}}.Save(filepath.Join(tmp, "cache.json")))

	old := &State{
		Papers:   []model.Paper{{DOI: doiEmpty}},
		Enriched: []model.EnrichedPaper{{DOI: doiEmpty}},
	}

	writeArtifacts(t, runDir, old, stageDiscovery, stageEnrichment)

	fb := &fakeAbstracts{abstracts: abstractsFrom(map[string]string{doiEmpty: epmcText})}
	p := newTest(t, Settings{Concurrency: 1, EnrichCacheTTL: ttl30d}, Clients{Abstracts: fb}, quietLog())
	opts := Options{RunDir: runDir, Resume: true, StopAfter: "2"}

	st := &State{}
	require.NoError(t, p.runStages(context.Background(), params(opts), st))

	assert.Equal(t, epmcText, st.Enriched[0].Abstract)

	var saved State

	require.NoError(t, loadJSON(filepath.Join(runDir, stageEnrichment+".json"), &saved))
	assert.Equal(t, epmcText, saved.Enriched[0].Abstract, "the artifact is rewritten")
	assert.Equal(t, srcEuroPMC, saved.Enriched[0].AbstractSource)

	cache := LoadEnrichCache(filepath.Join(tmp, "cache.json"), ttl30d, testNow, quietLog())
	assert.Equal(t, epmcText, cache[doiEmpty].Abstract, "the cache is rewritten")

	// the abstract is on disk now: a second resume has nothing to ask for
	st2 := &State{}
	require.NoError(t, p.runStages(context.Background(), params(opts), st2))
	assert.Equal(t, 1, fb.calls.get("Abstracts"))
}

// With the critique already done the old abstracts stay: the critiques were
// made on them.
func TestResumeLeavesFinishedCritiqueAlone(t *testing.T) {
	tmp := t.TempDir()
	runDir := filepath.Join(tmp, "run")
	done := &State{Enriched: []model.EnrichedPaper{{DOI: doiEmpty}}}

	writeArtifacts(t, runDir, done, stageDiscovery, stageEnrichment, stageCritique)

	fb := &fakeAbstracts{abstracts: abstractsFrom(map[string]string{doiEmpty: epmcText})}
	p := newTest(t, Settings{Concurrency: 1, EnrichCacheTTL: ttl30d}, Clients{Abstracts: fb}, quietLog())

	st := &State{}
	require.NoError(t, p.runStages(context.Background(),
		params(Options{RunDir: runDir, Resume: true, StopAfter: "3", TextMode: TextLean}), st))

	assert.Zero(t, fb.calls.total())
	assert.Empty(t, st.Enriched[0].Abstract)
}

// A resumed run keeps going when the fallback is down.
func TestResumeFallbackFailureIsAWarning(t *testing.T) {
	tmp := t.TempDir()
	runDir := filepath.Join(tmp, "run")

	writeArtifacts(t, runDir, &State{Enriched: []model.EnrichedPaper{{DOI: doiEmpty}}},
		stageDiscovery, stageEnrichment)

	fb := &fakeAbstracts{abstracts: func([]string) (map[string]string, error) { return nil, errors.New("down") }}
	p := newTest(t, Settings{Concurrency: 1, EnrichCacheTTL: ttl30d}, Clients{Abstracts: fb}, quietLog())

	st := &State{}
	require.NoError(t, p.runStages(context.Background(),
		params(Options{RunDir: runDir, Resume: true, StopAfter: "2"}), st))

	require.Len(t, st.Warnings, 1)
	assert.Contains(t, st.Warnings[0], "Europe PMC unavailable")
}

// writeArtifacts saves the same state as each named stage's artifact.
func writeArtifacts(t *testing.T, dir string, st *State, stages ...string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o750))

	for _, s := range stages {
		require.NoError(t, saveJSON(filepath.Join(dir, s+".json"), st))
	}
}
