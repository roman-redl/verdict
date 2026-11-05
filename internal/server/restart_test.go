package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/db"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
	"github.com/roman-redl/verdict/internal/server"
)

// get returns the status and body of a GET to the test server.
func get(t *testing.T, url string) (status int, body string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	return resp.StatusCode, string(raw)
}

func post(t *testing.T, url, body string) server.Job {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { require.NoError(t, resp.Body.Close()) }()

	var job server.Job
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&job))

	return job
}

// The daemon end to end over a real SQLite file: a finished job and one cut
// short by the shutdown are both there after a restart, the unfinished one
// marked interrupted, and new ids continue the sequence.
func TestJobsSurviveADaemonRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdict.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	clock := func() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC) }
	started := make(chan struct{}, 1)

	// first process: one quick job finishes, a slow one is running at shutdown
	run1 := func(ctx context.Context, opts pipeline.Options) (*pipeline.State, string, error) {
		if opts.Question == "slow" {
			started <- struct{}{}

			<-ctx.Done()

			return nil, "runs/partial", ctx.Err()
		}

		return &pipeline.State{Report: &model.Report{Verdict: "strong"}}, "runs/quick", nil
	}

	d1, err := db.Open(t.Context(), path)
	require.NoError(t, err)

	svc1, err := server.New(t.Context(), run1, d1.Jobs(clock), t.TempDir(), log)
	require.NoError(t, err)

	workCtx, stop := context.WithCancel(context.Background())

	var wg sync.WaitGroup

	wg.Go(func() { svc1.Work(workCtx) })

	srv1 := httptest.NewServer(svc1.Handler())

	quick := post(t, srv1.URL+"/runs", `{"question":"quick"}`)
	require.Eventually(t, func() bool {
		_, body := get(t, srv1.URL+"/runs/"+quick.ID)
		return strings.Contains(body, `"done"`)
	}, 5*time.Second, 20*time.Millisecond)

	slow := post(t, srv1.URL+"/runs", `{"question":"slow"}`)

	<-started
	post(t, srv1.URL+"/runs", `{"question":"never started"}`)

	stop() // the shutdown signal
	wg.Wait()
	srv1.Close()
	require.NoError(t, d1.Close())

	// second process on the same file
	d2, err := db.Open(t.Context(), path)
	require.NoError(t, err)

	defer func() { require.NoError(t, d2.Close()) }()

	svc2, err := server.New(t.Context(), run1, d2.Jobs(clock), t.TempDir(), log)
	require.NoError(t, err)

	srv2 := httptest.NewServer(svc2.Handler())
	defer srv2.Close()

	status, body := get(t, srv2.URL+"/runs")
	require.Equal(t, http.StatusOK, status)

	var jobs []server.Job
	require.NoError(t, json.Unmarshal([]byte(body), &jobs))
	require.Len(t, jobs, 3)

	assert.Equal(t, server.Job{
		ID: quick.ID, Status: server.JobDone, Question: "quick", Dir: "runs/quick", Verdict: "strong",
	}, jobs[0])
	assert.Equal(t, slow.ID, jobs[1].ID)
	assert.Equal(t, server.JobInterrupted, jobs[1].Status)
	assert.Equal(t, "runs/partial", jobs[1].Dir, "the partial run directory is kept for --resume")
	assert.Contains(t, jobs[1].Error, "--resume")
	assert.Equal(t, server.JobInterrupted, jobs[2].Status, "a job that never started is interrupted, not re-run")

	next := post(t, srv2.URL+"/runs", `{"question":"after the restart"}`)
	assert.Equal(t, "4", next.ID, "ids continue across the restart")
}
