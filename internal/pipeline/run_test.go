package pipeline

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
)

const (
	batchReply = `{"critiques":[` +
		`{"doi":"10.1000/a","design_quality":"high","relevance":"direct","result_direction":"positive"},` +
		`{"doi":"10.1000/b","design_quality":"low","relevance":"indirect","result_direction":"null"}]}`
	synthesisReply = `{"verdict":"moderate","verdict_rationale":"10.1000/a leads",` +
		`"consensus_status":"emerging","consensus":"leans positive",` +
		`"matrix_notes":{"10.1000/a":"the pivotal trial"},"gaps":["few studies"]}`
)

// runFakes wires a whole pipeline over fakes: two papers known to OpenAlex,
// Scite tallies for both, and a scripted LLM.
func runFakes(t *testing.T, replies ...string) (*Pipeline, *fakeWorks, *scriptCompleter) {
	t.Helper()

	works := &fakeWorks{byDOI: func(doi string) (*model.Work, error) {
		w := work(doi, "Study "+doi)
		w.Abstract = "A randomized trial of " + doi
		w.OAURL = "https://oa.test/" + doi + ".pdf"

		return w, nil
	}}
	citations := &fakeCitations{tallies: func(dois []string) (map[string]model.CitationTally, error) {
		out := map[string]model.CitationTally{}
		for _, d := range dois {
			out[d] = model.CitationTally{DOI: d, Total: 10, Supporting: 4, Contradicting: 1}
		}

		return out, nil
	}}
	llm := &scriptCompleter{replies: replies}
	p := newTest(t, Settings{
		MaxPapers: 10, Concurrency: 2, LLMConcurrency: 1, LLMChunk: 5, HybridPivotalMax: 2,
		CritiqueCacheTTL: ttl30d,
	}, Clients{
		Works: works, Citations: citations, LLM: llm,
		PDFs:    &fakePDFs{fetch: func(string) ([]byte, error) { return []byte("%PDF-1.4 stub"), nil }},
		PDFText: fakePDFText{text: "FULL TEXT"},
	}, quietLog())

	return p, works, llm
}

// The whole pipeline, four stages, on fakes: no HTTP server anywhere.
func TestRunAllStages(t *testing.T) {
	p, _, llm := runFakes(t, batchReply, synthesisReply)
	runDir := filepath.Join(t.TempDir(), "run")

	st, dir, err := p.Run(t.Context(), Options{
		DOIFile: doiList(t, listDOIA, listDOIB), RunDir: runDir, TextMode: TextLean,
	})
	require.NoError(t, err)
	require.Equal(t, runDir, dir)

	require.NotNil(t, st.Report)
	assert.Equal(t, "moderate", st.Report.Verdict)
	require.Len(t, st.Report.Matrix, 2)
	assert.Equal(t, "the pivotal trial", st.Report.Matrix[0].Note)
	assert.Equal(t, 4, st.Report.Matrix[0].Supporting, "the matrix numbers come from Scite, not the model")

	assert.Equal(t, 2, llm.calls, "one batched critique + one synthesis")
	require.NotNil(t, st.Cost)
	assert.Equal(t, 1, st.Cost.Critique.Calls)
	assert.Equal(t, 1, st.Cost.Synthesis.Calls)

	for _, name := range []string{
		"0-input.txt", "1-discovery.json", "2-enrichment.json", "3-critique.json", "4-synthesis.json",
		"report.md", "matrix.csv", "context.md",
	} {
		assert.FileExists(t, filepath.Join(dir, name))
	}

	//nolint:gosec // reads a file the run just wrote into the test's temp directory
	report, err := os.ReadFile(filepath.Join(runDir, "report.md"))
	require.NoError(t, err)
	assert.Contains(t, string(report), "MODERATE")

	// the checkpoint round-trips through LoadState
	loaded, err := LoadState(dir)
	require.NoError(t, err)
	assert.Equal(t, "moderate", loaded.Report.Verdict)
}

// --resume reloads the finished stages from disk and runs only the rest: the
// paid sources are not asked twice.
func TestRunResumeSkipsFinishedStages(t *testing.T) {
	p, works, llm := runFakes(t, batchReply, synthesisReply)
	runDir := filepath.Join(t.TempDir(), "run")
	list := doiList(t, listDOIA, listDOIB)

	// deterministic stages only
	st, _, err := p.Run(t.Context(), Options{DOIFile: list, RunDir: runDir, StopAfter: "2", TextMode: TextLean})
	require.NoError(t, err)
	require.Nil(t, st.Report)
	require.Zero(t, llm.calls, "no LLM before stage 3")

	lookups := works.calls.total()
	require.Positive(t, lookups)

	st, _, err = p.Run(t.Context(), Options{DOIFile: list, RunDir: runDir, Resume: true, TextMode: TextLean})
	require.NoError(t, err)

	require.NotNil(t, st.Report)
	assert.Equal(t, lookups, works.calls.total(), "stages 1-2 were loaded from their artifacts")
	assert.Equal(t, 2, llm.calls)
}

// Time comes from the injected clock: the run directory name, the report
// stamp and the cache entries all follow it.
func TestRunUsesTheInjectedClock(t *testing.T) {
	t.Chdir(t.TempDir())

	p, _, _ := runFakes(t, batchReply, synthesisReply)

	st, dir, err := p.Run(t.Context(), Options{
		DOIFile: doiList(t, listDOIA, listDOIB), Topic: "Creatine", TextMode: TextLean,
	})
	require.NoError(t, err)

	assert.Equal(t, testNow, st.Report.GeneratedAt)
	assert.Equal(t, filepath.Join("runs", "creatine"), filepath.Dir(dir))
	assert.Contains(t, filepath.Base(dir), "20261101-120000", "the directory stamp is the pinned time")

	cache := LoadCritiqueCache(filepath.Join("runs", "creatine", "critique-cache.json"), ttl30d, testNow, quietLog())
	require.NotEmpty(t, cache)

	for _, e := range cache {
		assert.Equal(t, testNow, e.CheckedAt)
	}

	// a month later the same entries are stale
	later := testNow.Add(31 * 24 * time.Hour)
	assert.Empty(t, LoadCritiqueCache(filepath.Join("runs", "creatine", "critique-cache.json"), ttl30d, later, quietLog()))
}

// A pipeline needs a logger and a clock.
func TestNewValidatesDependencies(t *testing.T) {
	_, err := New(Settings{}, Clients{}, nil, time.Now)
	require.Error(t, err)

	_, err = New(Settings{}, Clients{}, quietLog(), nil)
	require.Error(t, err)
}

// A stop-after run still leaves the digest for Q&A.
func TestRunStopAfterWritesContext(t *testing.T) {
	p, _, _ := runFakes(t)
	runDir := filepath.Join(t.TempDir(), "run")

	_, _, err := p.Run(t.Context(), Options{DOIFile: doiList(t, listDOIA), RunDir: runDir, StopAfter: "1-discovery"})
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(runDir, "context.md"))
	assert.NoFileExists(t, filepath.Join(runDir, "report.md"))
	assert.NoFileExists(t, filepath.Join(runDir, "2-enrichment.json"))
}

const recritiqueReply = `{"critiques":[{"doi":"10.1000/a","design_quality":"high","relevance":"direct",` +
	`"result_direction":"negative","concerns":["dropout hidden in the methods"]}]}`

// loadCritiqueArtifact reads the critique-stage artifact of a run directory
// back into a state.
func loadCritiqueArtifact(t *testing.T, runDir string) *State {
	t.Helper()

	st := &State{}
	require.NoError(t, loadJSON(filepath.Join(runDir, stageCritique+".json"), st))

	return st
}

// Hybrid: abstracts critique, then full texts for the pivotal study and a
// re-critique of it (all inside the critique stage), then ONE synthesis on the
// final critiques.
func TestRunHybridSynthesizesOnce(t *testing.T) {
	p, _, llm := runFakes(t, batchReply, recritiqueReply, synthesisReply)
	runDir := filepath.Join(t.TempDir(), "run")

	st, _, err := p.Run(t.Context(), Options{
		DOIFile: doiList(t, listDOIA, listDOIB), RunDir: runDir, TextMode: TextHybrid,
	})
	require.NoError(t, err)

	assert.Equal(t, 3, llm.calls, "critique, re-critique of the pivotal study, one synthesis")
	assert.Contains(t, llm.prompts[2], "dropout hidden", "the synthesis sees the deepened critique")
	assert.True(t, st.HybridDone)
	assert.Equal(t, "FULL TEXT", st.Enriched[0].FullText, "the pivotal study read in full")
	assert.Empty(t, st.Enriched[1].FullText, "the indirect study stays on its abstract")
	assert.Equal(t, 2, st.Cost.Critique.Calls)
	assert.Equal(t, 1, st.Cost.Synthesis.Calls)
	assert.FileExists(t, filepath.Join(runDir, "fulltext", "10.1000_a.pdf"))

	crit := loadCritiqueArtifact(t, runDir)
	assert.True(t, crit.HybridDone, "the critique artifact records the finished deepening")
	assert.Nil(t, crit.Report, "the critique artifact predates the synthesis")
	assert.Equal(t, "FULL TEXT", crit.Enriched[0].FullText)

	// resuming a finished state does not repeat any pass
	p2, _, llm2 := runFakes(t)

	_, _, err = p2.Run(t.Context(), Options{
		DOIFile: doiList(t, listDOIA, listDOIB), RunDir: runDir, Resume: true, TextMode: TextHybrid,
	})
	require.NoError(t, err)
	assert.Zero(t, llm2.calls)
}

// No pivotal study yields a full text: the deepening is a no-op that still
// counts as run, the abstracts critiques stand and the synthesis runs once.
func TestRunHybridWithoutFullTextSynthesizesOnce(t *testing.T) {
	p, _, llm := runFakes(t, batchReply, synthesisReply)
	p.clients.PDFs = &fakePDFs{fetch: func(string) ([]byte, error) { return nil, errors.New("not a PDF") }}
	runDir := filepath.Join(t.TempDir(), "run")

	st, _, err := p.Run(t.Context(), Options{
		DOIFile: doiList(t, listDOIA, listDOIB), RunDir: runDir, TextMode: TextHybrid,
	})
	require.NoError(t, err)

	assert.Equal(t, 2, llm.calls, "critique and one synthesis, no re-critique")
	assert.True(t, st.HybridDone, "the pass ran, even though there was nothing to deepen")
	assert.Empty(t, st.Enriched[0].FullText)
	assert.Equal(t, 1, st.Cost.Critique.Calls)
	assert.Equal(t, 1, st.Cost.Synthesis.Calls)
	assert.True(t, loadCritiqueArtifact(t, runDir).HybridDone)
}

// A critique artifact without HybridDone and without a synthesis (written by
// older code, or by a run stopped after the abstracts critique) gets the
// deepening on --resume, before the single synthesis; the artifact is saved
// again.
func TestRunResumeDeepensOldStyleCritiqueArtifact(t *testing.T) {
	list := doiList(t, listDOIA, listDOIB)
	runDir := filepath.Join(t.TempDir(), "run")

	// lean through the critique: the artifact has HybridDone == false
	p1, _, llm1 := runFakes(t, batchReply)
	_, _, err := p1.Run(t.Context(), Options{DOIFile: list, RunDir: runDir, StopAfter: "3", TextMode: TextLean})
	require.NoError(t, err)
	require.Equal(t, 1, llm1.calls)
	require.False(t, loadCritiqueArtifact(t, runDir).HybridDone)
	require.NoFileExists(t, filepath.Join(runDir, stageSynthesis+".json"))

	p2, _, llm2 := runFakes(t, recritiqueReply, synthesisReply)

	st, _, err := p2.Run(t.Context(), Options{DOIFile: list, RunDir: runDir, Resume: true, TextMode: TextHybrid})
	require.NoError(t, err)

	assert.Equal(t, 2, llm2.calls, "re-critique of the pivotal study, one synthesis")
	assert.Contains(t, llm2.prompts[1], "dropout hidden", "the synthesis sees the deepened critique")
	assert.True(t, st.HybridDone)
	assert.Equal(t, "FULL TEXT", st.Enriched[0].FullText)
	assert.Equal(t, 1, st.Cost.Synthesis.Calls)

	crit := loadCritiqueArtifact(t, runDir)
	assert.True(t, crit.HybridDone, "3-critique.json is saved again after the deepening")
	assert.Equal(t, "FULL TEXT", crit.Enriched[0].FullText)
}

// A resume never repeats the pass once the synthesis exists, and a --stop-after
// at an earlier stage starts nothing after it.
func TestRunResumeLeavesFinishedRunsAlone(t *testing.T) {
	list := doiList(t, listDOIA, listDOIB)
	runDir := filepath.Join(t.TempDir(), "run")

	// a complete lean run: HybridDone == false, the synthesis artifact present
	p1, _, _ := runFakes(t, batchReply, synthesisReply)
	_, _, err := p1.Run(t.Context(), Options{DOIFile: list, RunDir: runDir, TextMode: TextLean})
	require.NoError(t, err)

	for name, stop := range map[string]string{"complete": "", "stop-after 2": "2"} {
		p, _, llm := runFakes(t)

		st, _, err := p.Run(t.Context(), Options{
			DOIFile: list, RunDir: runDir, Resume: true, StopAfter: stop, TextMode: TextHybrid,
		})
		require.NoError(t, err, name)

		assert.Zero(t, llm.calls, name)
		assert.False(t, st.HybridDone, name)
		assert.Empty(t, st.Enriched[0].FullText, name)
	}
}

// A usage limit during the deepening ends the run with the same user error as
// in the abstracts critique: no synthesis, and the critique artifact is not
// saved as a finished stage.
func TestRunHybridStopDuringDeepening(t *testing.T) {
	p, _, _ := runFakes(t)
	stop := &stopCompleter{reply: func(call int, _ string) (string, error) {
		if call == 1 {
			return batchReply, nil
		}

		return "", fmt.Errorf("usage limit reached: %w", ErrLLMStop)
	}}
	p.clients.LLM = stop
	runDir := filepath.Join(t.TempDir(), "run")

	_, _, err := p.Run(t.Context(), Options{
		DOIFile: doiList(t, listDOIA, listDOIB), RunDir: runDir, TextMode: TextHybrid,
	})
	requireStopError(t, err)

	assert.Equal(t, 2, stop.count(), "the abstracts critique and the refused re-critique, nothing after")
	assert.NoFileExists(t, filepath.Join(runDir, stageCritique+".json"))
	assert.NoFileExists(t, filepath.Join(runDir, stageSynthesis+".json"))
}

// Run validates its input before touching any source.
func TestRunRejectsBadInput(t *testing.T) {
	p, works, _ := runFakes(t)

	_, _, err := p.Run(t.Context(), Options{RunDir: t.TempDir(), TextMode: "deep"})

	var ue *UserError

	require.ErrorAs(t, err, &ue)
	assert.Contains(t, ue.Msg, "text mode")

	_, _, err = p.Run(t.Context(), Options{RunDir: t.TempDir()})
	require.ErrorAs(t, err, &ue)
	assert.Zero(t, works.calls.total())
}

// Without an LLM backend the verdict cannot be built, and says so.
func TestRunWithoutLLMFailsAtSynthesis(t *testing.T) {
	p := newTest(t, Settings{MaxPapers: 5, Concurrency: 1}, Clients{Works: &fakeWorks{}}, quietLog())

	_, _, err := p.Run(t.Context(), Options{DOIFile: doiList(t, listDOIA), RunDir: t.TempDir(), TextMode: TextLean})
	require.ErrorContains(t, err, "requires an LLM backend")
}
