package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// startServer wires a Server around the fake runner, starts the worker and
// the HTTP test server, and tears everything down with the test.
func startServer(t *testing.T, run Runner) *httptest.Server {
	t.Helper()

	svc, err := New(run, t.TempDir(), quietLog())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup

	wg.Go(func() { svc.Work(ctx) })

	srv := httptest.NewServer(svc.Handler())

	t.Cleanup(func() {
		srv.Close()
		cancel()
		wg.Wait()
	})

	return srv
}

func TestSubmitRunLifecycle(t *testing.T) {
	srv := startServer(t, func(_ context.Context, opts pipeline.Options) (*pipeline.State, string, error) {
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "report.md"), []byte("# Verdict: moderate"), 0o600)
		require.NoError(t, err)

		assert.NotEmpty(t, opts.Question, "question must be defaulted before the runner sees it")

		return &pipeline.State{Report: &model.Report{Verdict: "moderate"}}, dir, nil
	})

	// invalid submit
	resp, err := postJSON(srv.URL+"/runs", stringsReader(`{}`))
	require.NoError(t, err)

	_ = resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "empty submit")

	// valid submit
	resp, err = postJSON(srv.URL+"/runs",
		stringsReader(`{"question":"does creatine help cognition","topic":"creatine"}`))
	require.NoError(t, err)

	var job Job

	_ = json.NewDecoder(resp.Body).Decode(&job)
	_ = resp.Body.Close()

	require.Equal(t, http.StatusAccepted, resp.StatusCode, "submit")
	require.NotEmpty(t, job.ID, "submit")
	require.Contains(t, []JobStatus{JobQueued, JobRunning}, job.Status, "submit")

	job = waitForJob(t, srv.URL, job.ID, JobDone, JobFailed)

	require.Equal(t, JobDone, job.Status, "job did not finish")
	require.Equal(t, "moderate", job.Verdict, "job did not finish")
	require.NotEmpty(t, job.Dir, "job did not finish")

	// artifact fetch
	resp, err = httpGet(srv.URL + "/runs/" + job.ID + "/report.md")
	require.NoError(t, err)

	body := make([]byte, 64)
	n, _ := resp.Body.Read(body)
	_ = resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "artifact")
	require.Equal(t, "# Verdict: moderate", string(body[:n]), "artifact")

	// whitelist: only run artifacts are served (the stdlib client
	// normalizes ".." away, so a forbidden plain name is the real check)
	resp, err = httpGet(srv.URL + "/runs/" + job.ID + "/verdict.db")
	require.NoError(t, err)

	_ = resp.Body.Close()

	require.Equal(t, http.StatusForbidden, resp.StatusCode, "non-artifact file")

	// unknown id
	resp, err = httpGet(srv.URL + "/runs/999")
	require.NoError(t, err)

	_ = resp.Body.Close()

	require.Equal(t, http.StatusNotFound, resp.StatusCode, "unknown id")
}

func TestListRuns(t *testing.T) {
	srv := startServer(t, func(_ context.Context, _ pipeline.Options) (*pipeline.State, string, error) {
		return nil, t.TempDir(), nil
	})

	resp, err := postJSON(srv.URL+"/runs",
		stringsReader(`{"dois":["10.1000/a"],"topic":"t"}`))
	require.NoError(t, err)

	var job Job

	_ = json.NewDecoder(resp.Body).Decode(&job)
	_ = resp.Body.Close()

	waitForJob(t, srv.URL, job.ID, JobDone, JobFailed)

	resp, err = httpGet(srv.URL + "/runs")
	require.NoError(t, err)

	var jobs []Job

	_ = json.NewDecoder(resp.Body).Decode(&jobs)
	_ = resp.Body.Close()

	require.Len(t, jobs, 1)
	require.Equal(t, "t", jobs[0].Topic)
}

// A client never reads err.Error() of an internal failure (paths, SQL, API
// answers); a request error keeps its own text.
func TestJobErrorsAreSafeForClients(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{
			name:    "internal error is generic",
			err:     fmt.Errorf("stage 2-enrichment: open /home/me/runs/x.json: %w", errors.New("secret token=abc")),
			wantMsg: "the run failed; details are in the daemon log",
		},
		{
			name:    "request error keeps its text",
			err:     fmt.Errorf("stage 1-discovery: %w", &pipeline.UserError{Msg: "unknown engine \"bing\""}),
			wantMsg: "unknown engine \"bing\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := startServer(t, func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
				return nil, "", tt.err
			})

			resp, err := postJSON(srv.URL+"/runs", stringsReader(`{"question":"q"}`))
			require.NoError(t, err)

			var job Job

			_ = json.NewDecoder(resp.Body).Decode(&job)
			_ = resp.Body.Close()

			job = waitForJob(t, srv.URL, job.ID, JobFailed)
			require.Equal(t, tt.wantMsg, job.Error)
		})
	}
}

// An unknown text mode is rejected up front, before a job is created.
func TestSubmitRejectsUnknownTextMode(t *testing.T) {
	srv := startServer(t, func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
		t.Error("the runner must not be reached")

		return nil, "", nil
	})

	resp, err := postJSON(srv.URL+"/runs", stringsReader(`{"question":"q","text_mode":"deep"}`))
	require.NoError(t, err)

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, string(body), "lean | hybrid | full")
}

// Parser details of a malformed body are not echoed back.
func TestSubmitMalformedJSONHidesParserDetails(t *testing.T) {
	srv := startServer(t, func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
		return nil, "", nil
	})

	resp, err := postJSON(srv.URL+"/runs", stringsReader(`{"question": `))
	require.NoError(t, err)

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.NotContains(t, string(body), "unexpected EOF")
}

// An oversized body is cut off by MaxBytesReader before it is decoded.
func TestSubmitRejectsOversizedBody(t *testing.T) {
	srv := startServer(t, func(_ context.Context, _ pipeline.Options) (*pipeline.State, string, error) {
		t.Error("the runner must not be reached")

		return nil, "", nil
	})

	huge := `{"question":"` + strings.Repeat("x", maxSubmitBytes) + `"}`

	resp, err := postJSON(srv.URL+"/runs", stringsReader(huge))
	require.NoError(t, err)

	_ = resp.Body.Close()

	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

// Cancelling the worker context (the shutdown signal) interrupts the
// running pipeline run and leaves Work returning — it must not wait for the
// run to finish on its own.
func TestShutdownInterruptsRunningJob(t *testing.T) {
	started := make(chan struct{})

	svc, err := New(func(ctx context.Context, _ pipeline.Options) (*pipeline.State, string, error) {
		close(started)
		<-ctx.Done()

		return nil, "", ctx.Err()
	}, t.TempDir(), quietLog())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		svc.Work(ctx)
	}()

	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()

	resp, err := postJSON(srv.URL+"/runs", stringsReader(`{"question":"q"}`))
	require.NoError(t, err)

	var job Job

	_ = json.NewDecoder(resp.Body).Decode(&job)
	_ = resp.Body.Close()

	<-started
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Work did not return after the context was canceled")
	}

	got, ok := svc.snapshot(job.ID)
	require.True(t, ok)
	require.Equal(t, JobInterrupted, got.Status)
}

// The queue is never closed, so a submit racing with shutdown cannot panic
// on a closed channel; it simply stays queued.
func TestSubmitAfterWorkerStoppedDoesNotPanic(t *testing.T) {
	svc, err := New(func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
		return nil, "", nil
	}, t.TempDir(), quietLog())
	require.NoError(t, err)

	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()

	resp, err := postJSON(srv.URL+"/runs", stringsReader(`{"question":"q"}`))
	require.NoError(t, err)

	_ = resp.Body.Close()

	require.Equal(t, http.StatusAccepted, resp.StatusCode)
}

// waitForJob polls GET /runs/{id} until the job reaches one of the statuses.
func waitForJob(t *testing.T, base, id string, statuses ...JobStatus) Job {
	t.Helper()

	var job Job

	require.Eventually(t, func() bool {
		resp, err := httpGet(base + "/runs/" + id)
		if err != nil {
			return false
		}

		defer func() { _ = resp.Body.Close() }()

		if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
			return false
		}

		return slices.Contains(statuses, job.Status)
	}, 5*time.Second, 20*time.Millisecond, "job %s never reached %v", id, statuses)

	return job
}

func stringsReader(s string) io.Reader { return strings.NewReader(s) }

// postJSON/httpGet — requests to the test server with an explicit context
// (noctx): the test's own context is not needed, the fake server answers at once.
func postJSON(url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, body)
	if err != nil {
		return nil, err //nolint:wrapcheck // test helper: the raw error is enough for require.NoError
	}

	req.Header.Set("Content-Type", "application/json")

	return http.DefaultClient.Do(req) //nolint:wrapcheck // test helper: the raw error is enough for require.NoError
}

func httpGet(url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err //nolint:wrapcheck // test helper: the raw error is enough for require.NoError
	}

	return http.DefaultClient.Do(req) //nolint:wrapcheck // test helper: the raw error is enough for require.NoError
}
