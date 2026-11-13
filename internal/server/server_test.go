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
	"strconv"
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

// memJobs is the job table in memory with the same conditional transitions
// as the SQLite one; err, when set, fails every call.
type memJobs struct {
	mu   sync.Mutex
	jobs []Job
	err  error
}

var _ Jobs = (*memJobs)(nil)

func (m *memJobs) Create(_ context.Context, j NewJob) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return Job{}, m.err
	}

	job := Job{ID: strconv.Itoa(len(m.jobs) + 1), Status: JobQueued, Question: j.Question, Topic: j.Topic}
	m.jobs = append(m.jobs, job)

	return job, nil
}

// setErr makes every following call fail (nil: heal).
func (m *memJobs) setErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.err = err
}

// all returns a copy of the table.
func (m *memJobs) all() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.jobs)
}

// find returns the index of a job; the caller holds the lock.
func (m *memJobs) find(id string) int {
	return slices.IndexFunc(m.jobs, func(j Job) bool { return j.ID == id })
}

func (m *memJobs) Start(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	i := m.find(id)
	if i < 0 || m.jobs[i].Status != JobQueued {
		return ErrConflict
	}

	m.jobs[i].Status = JobRunning

	return nil
}

func (m *memJobs) Finish(_ context.Context, id string, o Outcome) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	i := m.find(id)
	if i < 0 || (m.jobs[i].Status != JobQueued && m.jobs[i].Status != JobRunning) {
		return ErrConflict
	}

	m.jobs[i].Status, m.jobs[i].Dir, m.jobs[i].Verdict, m.jobs[i].Error = o.Status, o.Dir, o.Verdict, o.Error

	return nil
}

func (m *memJobs) Get(_ context.Context, id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return Job{}, m.err
	}

	i := m.find(id)
	if i < 0 {
		return Job{}, ErrNotFound
	}

	return m.jobs[i], nil
}

func (m *memJobs) List(context.Context) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return nil, m.err
	}

	return slices.Clone(m.jobs), nil
}

func (m *memJobs) InterruptUnfinished(_ context.Context, reason string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n := 0

	for i := range m.jobs {
		if s := m.jobs[i].Status; s == JobQueued || s == JobRunning {
			m.jobs[i].Status, m.jobs[i].Error = JobInterrupted, reason
			n++
		}
	}

	return n, nil
}

// startServer wires a Server around the fake runner and the in-memory table,
// starts the worker and the HTTP test server, and tears everything down with
// the test.
func startServer(t *testing.T, run Runner) (*httptest.Server, *memJobs) {
	t.Helper()

	jobs := &memJobs{}

	svc, err := New(t.Context(), run, jobs, t.TempDir(), quietLog())
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

	return srv, jobs
}

// submit POSTs a JSON body and decodes the job the daemon answers with.
func submit(t *testing.T, base, body string) (job Job, status int) {
	t.Helper()

	resp, err := call(http.MethodPost, base+"/runs", strings.NewReader(body))
	require.NoError(t, err)

	defer func() { require.NoError(t, resp.Body.Close()) }()

	if resp.StatusCode == http.StatusAccepted {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&job))
	}

	return job, resp.StatusCode
}

func TestSubmitRunLifecycle(t *testing.T) {
	srv, _ := startServer(t, func(_ context.Context, opts pipeline.Options) (*pipeline.State, string, error) {
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "report.md"), []byte("# Verdict: moderate"), 0o600)
		require.NoError(t, err)

		assert.NotEmpty(t, opts.Question, "question must be defaulted before the runner sees it")

		return &pipeline.State{Report: &model.Report{Verdict: "moderate"}}, dir, nil
	})

	_, status := submit(t, srv.URL, `{}`)
	require.Equal(t, http.StatusBadRequest, status, "empty submit")

	job, status := submit(t, srv.URL, `{"question":"does creatine help cognition","topic":"creatine"}`)
	require.Equal(t, http.StatusAccepted, status, "submit")
	require.NotEmpty(t, job.ID, "submit")
	require.Contains(t, []JobStatus{JobQueued, JobRunning, JobDone}, job.Status, "submit")

	job = waitForJob(t, srv.URL, job.ID, JobDone, JobFailed)

	require.Equal(t, JobDone, job.Status, "job did not finish")
	require.Equal(t, "moderate", job.Verdict, "job did not finish")
	require.NotEmpty(t, job.Dir, "job did not finish")

	// artifact fetch
	status, body := fetch(t, srv.URL+"/runs/"+job.ID+"/report.md")
	require.Equal(t, http.StatusOK, status, "artifact")
	require.Equal(t, "# Verdict: moderate", body, "artifact")

	// whitelist: only run artifacts are served (the stdlib client
	// normalizes ".." away, so a forbidden plain name is the real check)
	status, _ = fetch(t, srv.URL+"/runs/"+job.ID+"/verdict.db")
	require.Equal(t, http.StatusForbidden, status, "non-artifact file")

	// a job without a directory has no artifacts, an unknown id no job
	status, _ = fetch(t, srv.URL+"/runs/999")
	require.Equal(t, http.StatusNotFound, status, "unknown id")

	status, _ = fetch(t, srv.URL+"/runs/999/report.md")
	require.Equal(t, http.StatusNotFound, status, "artifact of an unknown id")
}

// Jobs come back oldest first, every time.
func TestListRuns(t *testing.T) {
	srv, _ := startServer(t, func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
		return nil, t.TempDir(), nil
	})

	for _, q := range []string{"first", "second", "third"} {
		job, status := submit(t, srv.URL, `{"question":"`+q+`"}`)
		require.Equal(t, http.StatusAccepted, status)
		waitForJob(t, srv.URL, job.ID, JobDone, JobFailed)
	}

	_, body := fetch(t, srv.URL+"/runs")

	var jobs []Job
	require.NoError(t, json.Unmarshal([]byte(body), &jobs))

	require.Len(t, jobs, 3)
	assert.Equal(t, []string{"first", "second", "third"}, []string{jobs[0].Question, jobs[1].Question, jobs[2].Question})
}

// A DOI-only submit gets the default title and the list lands on disk where
// the pipeline reads it.
func TestSubmitDOIList(t *testing.T) {
	var got pipeline.Options

	srv, _ := startServer(t, func(_ context.Context, opts pipeline.Options) (*pipeline.State, string, error) {
		got = opts

		return nil, t.TempDir(), nil
	})

	job, status := submit(t, srv.URL, `{"dois":["10.1000/a","10.1000/b"],"topic":"t","text_mode":"lean"}`)
	require.Equal(t, http.StatusAccepted, status)
	assert.Equal(t, defaultDOIListQuestion, job.Question)

	waitForJob(t, srv.URL, job.ID, JobDone)

	assert.Equal(t, pipeline.TextLean, got.TextMode)
	assert.Equal(t, "t", got.Topic)

	list, err := os.ReadFile(got.DOIFile)
	require.NoError(t, err)
	assert.Equal(t, "10.1000/a\n10.1000/b\n", string(list))
}

// An oversized body is cut off by MaxBytesReader before it is decoded.
func TestSubmitRejectsOversizedBody(t *testing.T) {
	srv, _ := startServer(t, func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
		t.Error("the runner must not be reached")

		return nil, "", nil
	})

	_, status := submit(t, srv.URL, `{"question":"`+strings.Repeat("x", maxSubmitBytes)+`"}`)
	require.Equal(t, http.StatusRequestEntityTooLarge, status)
}

// An unknown text mode is rejected up front, before a job is created.
func TestSubmitRejectsUnknownTextMode(t *testing.T) {
	srv, jobs := startServer(t, func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
		t.Error("the runner must not be reached")

		return nil, "", nil
	})

	resp, err := call(http.MethodPost, srv.URL+"/runs", strings.NewReader(`{"question":"q","text_mode":"deep"}`))
	require.NoError(t, err)

	body, _ := io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, string(body), "lean | hybrid | full")
	require.Empty(t, jobs.all(), "a refused request leaves no job behind")
}

// A job carries the report language of its request; an absent one is left for
// the pipeline's default (REPORT_LANG, then English), an unknown one is
// refused before a job is created.
func TestSubmitReportLanguage(t *testing.T) {
	var got pipeline.Options

	srv, jobs := startServer(t, func(_ context.Context, opts pipeline.Options) (*pipeline.State, string, error) {
		got = opts

		return nil, t.TempDir(), nil
	})

	job, status := submit(t, srv.URL, `{"question":"q","lang":"RU"}`)
	require.Equal(t, http.StatusAccepted, status)
	waitForJob(t, srv.URL, job.ID, JobDone)
	assert.Equal(t, model.LangRU, got.Language)

	job, status = submit(t, srv.URL, `{"question":"q"}`)
	require.Equal(t, http.StatusAccepted, status)
	waitForJob(t, srv.URL, job.ID, JobDone)
	assert.Equal(t, model.Language(""), got.Language)

	before := len(jobs.all())
	resp, err := call(http.MethodPost, srv.URL+"/runs", strings.NewReader(`{"question":"q","lang":"de"}`))
	require.NoError(t, err)

	body, _ := io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, string(body), "en | ru")
	require.Len(t, jobs.all(), before, "a refused request leaves no job behind")
}

// Parser details of a malformed body are not echoed back.
func TestSubmitMalformedJSONHidesParserDetails(t *testing.T) {
	srv, _ := startServer(t, func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
		return nil, "", nil
	})

	resp, err := call(http.MethodPost, srv.URL+"/runs", strings.NewReader(`{"question": `))
	require.NoError(t, err)

	body, _ := io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.NotContains(t, string(body), "unexpected EOF")
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
			srv, _ := startServer(t, func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
				return nil, "", tt.err
			})

			job, _ := submit(t, srv.URL, `{"question":"q"}`)

			job = waitForJob(t, srv.URL, job.ID, JobFailed)
			require.Equal(t, tt.wantMsg, job.Error)
		})
	}
}

// Cancelling the worker context (the shutdown signal) interrupts the running
// pipeline run and leaves Work returning — it must not wait for the run to
// finish on its own — and the outcome is still stored.
func TestShutdownInterruptsRunningJob(t *testing.T) {
	started := make(chan struct{})
	jobs := &memJobs{}

	svc, err := New(t.Context(), func(ctx context.Context, _ pipeline.Options) (*pipeline.State, string, error) {
		close(started)
		<-ctx.Done()

		return nil, "runs/partial", ctx.Err()
	}, jobs, t.TempDir(), quietLog())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		svc.Work(ctx)
	}()

	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()

	job, status := submit(t, srv.URL, `{"question":"q"}`)
	require.Equal(t, http.StatusAccepted, status)

	<-started
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Work did not return after the context was canceled")
	}

	got, err := jobs.Get(t.Context(), job.ID)
	require.NoError(t, err)
	require.Equal(t, JobInterrupted, got.Status)
	require.Equal(t, "runs/partial", got.Dir, "the partial run directory stays reachable for --resume")
	require.Contains(t, got.Error, "--resume")
}

// A submit racing with shutdown cannot panic on a closed channel: the queue
// is never closed, the job simply stays queued.
func TestSubmitAfterWorkerStoppedDoesNotPanic(t *testing.T) {
	svc, err := New(t.Context(), func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
		return nil, "", nil
	}, &memJobs{}, t.TempDir(), quietLog())
	require.NoError(t, err)

	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()

	job, status := submit(t, srv.URL, `{"question":"q"}`)
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, JobQueued, job.Status)
}

// A full queue rejects the job instead of blocking the client.
func TestSubmitRejectsWhenQueueIsFull(t *testing.T) {
	svc, err := New(t.Context(), func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
		return nil, "", nil
	}, &memJobs{}, t.TempDir(), quietLog()) // no worker: nothing drains the queue
	require.NoError(t, err)

	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()

	for range queueSize {
		job, status := submit(t, srv.URL, `{"question":"q"}`)
		require.Equal(t, http.StatusAccepted, status)
		require.Equal(t, JobQueued, job.Status)
	}

	job, status := submit(t, srv.URL, `{"question":"one too many"}`)
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, JobFailed, job.Status)
	require.Equal(t, "queue full", job.Error)
}

// Leftovers of the previous process are settled when the server starts.
func TestNewInterruptsUnfinishedJobs(t *testing.T) {
	jobs := &memJobs{jobs: []Job{
		{ID: "1", Status: JobDone, Verdict: "weak"},
		{ID: "2", Status: JobRunning},
		{ID: "3", Status: JobQueued},
		{ID: "4", Status: JobFailed, Error: "x"},
	}}

	_, err := New(t.Context(), func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
		return nil, "", nil
	}, jobs, t.TempDir(), quietLog())
	require.NoError(t, err)

	all := jobs.all()

	statuses := make([]JobStatus, 0, len(all))
	for _, j := range all {
		statuses = append(statuses, j.Status)
	}

	assert.Equal(t, []JobStatus{JobDone, JobInterrupted, JobInterrupted, JobFailed}, statuses)
	assert.Contains(t, jobs.all()[1].Error, "stopped before the job finished")
}

// A store failure is a generic 500; the cause stays in the log.
func TestStoreFailuresAreGeneric500(t *testing.T) {
	srv, jobs := startServer(t, func(context.Context, pipeline.Options) (*pipeline.State, string, error) {
		return nil, "", nil
	})

	jobs.setErr(errors.New("disk I/O error at /var/lib/verdict/runs/verdict.db"))

	for _, url := range []string{srv.URL + "/runs", srv.URL + "/runs/1", srv.URL + "/runs/1/report.md"} {
		status, body := fetch(t, url)
		require.Equal(t, http.StatusInternalServerError, status, url)
		require.NotContains(t, body, "verdict.db", url)
	}

	resp, err := call(http.MethodPost, srv.URL+"/runs", strings.NewReader(`{"question":"q"}`))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestNewValidatesDependencies(t *testing.T) {
	run := func(context.Context, pipeline.Options) (*pipeline.State, string, error) { return nil, "", nil }

	_, err := New(t.Context(), nil, &memJobs{}, "", quietLog())
	require.Error(t, err)

	_, err = New(t.Context(), run, nil, "", quietLog())
	require.Error(t, err)

	_, err = New(t.Context(), run, &memJobs{}, "", nil)
	require.Error(t, err)

	_, err = New(t.Context(), run, &memJobs{}, "", quietLog())
	require.NoError(t, err)
}

// waitForJob polls GET /runs/{id} until the job reaches one of the statuses.
func waitForJob(t *testing.T, base, id string, statuses ...JobStatus) Job {
	t.Helper()

	var job Job

	require.Eventually(t, func() bool {
		status, body := fetch(t, base+"/runs/"+id)
		if status != http.StatusOK {
			return false
		}

		if err := json.Unmarshal([]byte(body), &job); err != nil {
			return false
		}

		return slices.Contains(statuses, job.Status)
	}, 5*time.Second, 20*time.Millisecond, "job %s never reached %v", id, statuses)

	return job
}

// fetch GETs a URL and returns the status and the body.
func fetch(t *testing.T, url string) (status int, body string) {
	t.Helper()

	resp, err := call(http.MethodGet, url, http.NoBody)
	require.NoError(t, err)

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	return resp.StatusCode, string(raw)
}

// call sends a request to a test server with an explicit context (noctx): the
// test's own context is not needed, the fake server answers at once.
func call(method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}

	if body != http.NoBody {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", url, err)
	}

	return resp, nil
}
