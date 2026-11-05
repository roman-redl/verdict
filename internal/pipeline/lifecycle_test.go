package pipeline

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doiList writes a DOI list file and returns its path.
func doiList(t *testing.T, dois ...string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dois.txt")

	var buf bytes.Buffer
	for _, d := range dois {
		buf.WriteString(d + "\n")
	}

	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))

	return path
}

// A canceled context (a shutdown signal) makes every source fail without
// failing the stage; the half-filled state must not be saved as a finished
// stage, or --resume would skip it.
func TestRunInterruptedDoesNotSaveStageArtifact(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := newTest(t, Settings{Concurrency: 2}, Clients{Works: &fakeWorks{}}, quietLog())

	runDir := filepath.Join(t.TempDir(), "run")
	_, _, err := p.Run(ctx, Options{DOIFile: doiList(t, "10.1000/a", "10.1000/b"), RunDir: runDir})

	require.ErrorIs(t, err, context.Canceled)
	assert.NoFileExists(t, filepath.Join(runDir, "1-discovery.json"))
}

// The run directory is a parameter of the run, not state of the instance: one
// Pipeline serves several runs, each writing into its own directory.
func TestOnePipelineServesTwoRuns(t *testing.T) {
	p := newTest(t, Settings{Concurrency: 1}, Clients{Works: &fakeWorks{}}, quietLog())
	list := doiList(t, "10.1000/a")

	base := t.TempDir()

	for i := range 2 {
		runDir := filepath.Join(base, "run-"+strconv.Itoa(i))

		_, got, err := p.Run(t.Context(), Options{DOIFile: list, RunDir: runDir, StopAfter: "1"})
		require.NoError(t, err)
		require.Equal(t, runDir, got)
		assert.FileExists(t, filepath.Join(runDir, "1-discovery.json"))
		assert.FileExists(t, filepath.Join(runDir, "0-input.txt"))
	}
}

// A corrupt cache is a cold start, but never a silent one: it costs API calls.
func TestCorruptCacheIsLogged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	require.NoError(t, os.WriteFile(path, []byte("{broken"), 0o600))

	var logs bytes.Buffer

	log := slog.New(slog.NewTextHandler(&logs, nil))

	assert.Empty(t, LoadEnrichCache(path, ttl30d, testNow, log))
	assert.Empty(t, LoadCritiqueCache(path, ttl30d, testNow, log))
	assert.Contains(t, logs.String(), "cache corrupt")
}
