package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

func TestSubmitRunLifecycle(t *testing.T) {
	handler, drain := New(func(_ context.Context, opts pipeline.Options) (*pipeline.State, string, error) {
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "report.md"), []byte("# Verdict: moderate"), 0o600)
		require.NoError(t, err)

		assert.NotEmpty(t, opts.Question, "question must be defaulted before the runner sees it")

		return &pipeline.State{Report: &model.Report{Verdict: "moderate"}}, dir, nil
	})
	defer drain()

	srv := httptest.NewServer(handler)
	defer srv.Close()

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
	require.Contains(t, []string{"queued", "running"}, job.Status, "submit")

	// wait for the worker
	deadline := time.Now().Add(5 * time.Second)

	for {
		resp, err = httpGet(srv.URL + "/runs/" + job.ID)
		require.NoError(t, err)

		_ = json.NewDecoder(resp.Body).Decode(&job)
		_ = resp.Body.Close()

		if job.Status == "done" || job.Status == "failed" || time.Now().After(deadline) {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	require.Equal(t, "done", job.Status, "job did not finish")
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
	handler, drain := New(func(_ context.Context, _ pipeline.Options) (*pipeline.State, string, error) {
		return nil, t.TempDir(), nil
	})
	defer drain()

	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := postJSON(srv.URL+"/runs",
		stringsReader(`{"dois":["10.1000/a"],"topic":"t"}`))
	require.NoError(t, err)

	_ = resp.Body.Close()

	time.Sleep(300 * time.Millisecond) // let the worker finish

	resp, err = httpGet(srv.URL + "/runs")
	require.NoError(t, err)

	var jobs []Job

	_ = json.NewDecoder(resp.Body).Decode(&jobs)
	_ = resp.Body.Close()

	require.Len(t, jobs, 1)
	require.Equal(t, "t", jobs[0].Topic)
}

func stringsReader(s string) io.Reader { return strings.NewReader(s) }

// postJSON/httpGet — запросы к тестовому серверу с явным контекстом
// (noctx): контекст теста здесь не нужен, ответ фейк-сервера мгновенный.
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
