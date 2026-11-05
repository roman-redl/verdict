package pipeline

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/openalex"
)

// A canceled context (a shutdown signal) makes every source fail without
// failing the stage; the half-filled state must not be saved as a finished
// stage, or --resume would skip it.
func TestRunInterruptedDoesNotSaveStageArtifact(t *testing.T) {
	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer oa.Close()

	dir := t.TempDir()
	list := filepath.Join(dir, "dois.txt")
	require.NoError(t, os.WriteFile(list, []byte("10.1000/a\n10.1000/b\n"), 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := New(Settings{Concurrency: 2}, Clients{OpenAlex: openalex.NewWithBase(oa.URL, "")}, quietLog())

	runDir := filepath.Join(dir, "run")
	_, _, err := p.Run(ctx, Options{DOIFile: list, RunDir: runDir})

	require.ErrorIs(t, err, context.Canceled)
	assert.NoFileExists(t, filepath.Join(runDir, "1-discovery.json"))
}

// A corrupt cache is a cold start, but never a silent one: it costs API calls.
func TestCorruptCacheIsLogged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	require.NoError(t, os.WriteFile(path, []byte("{broken"), 0o600))

	var logs bytes.Buffer

	log := slog.New(slog.NewTextHandler(&logs, nil))

	assert.Empty(t, LoadEnrichCache(path, ttl30d, log))
	assert.Empty(t, LoadCritiqueCache(path, ttl30d, log))
	assert.Contains(t, logs.String(), "cache corrupt")
}
