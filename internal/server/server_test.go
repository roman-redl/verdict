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

	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

func TestSubmitRunLifecycle(t *testing.T) {
	handler, drain := New(func(_ context.Context, opts pipeline.Options) (*pipeline.State, string, error) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte("# Verdict: moderate"), 0o644); err != nil {
			t.Fatal(err)
		}
		if opts.Question == "" {
			t.Error("question must be defaulted before the runner sees it")
		}
		return &pipeline.State{Report: &model.Report{Verdict: "moderate"}}, dir, nil
	})
	defer drain()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// invalid submit
	resp, err := http.Post(srv.URL+"/runs", "application/json",
		stringsReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty submit: %d, want 400", resp.StatusCode)
	}

	// valid submit
	resp, err = http.Post(srv.URL+"/runs", "application/json",
		stringsReader(`{"question":"does creatine help cognition","topic":"creatine"}`))
	if err != nil {
		t.Fatal(err)
	}
	var job Job
	_ = json.NewDecoder(resp.Body).Decode(&job)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || job.ID == "" ||
		(job.Status != "queued" && job.Status != "running") {
		t.Fatalf("submit: %d %+v", resp.StatusCode, job)
	}

	// wait for the worker
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err = http.Get(srv.URL + "/runs/" + job.ID)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewDecoder(resp.Body).Decode(&job)
		resp.Body.Close()
		if job.Status == "done" || job.Status == "failed" || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != "done" || job.Verdict != "moderate" || job.Dir == "" {
		t.Fatalf("job did not finish: %+v", job)
	}

	// artifact fetch
	resp, err = http.Get(srv.URL + "/runs/" + job.ID + "/report.md")
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 64)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body[:n]) != "# Verdict: moderate" {
		t.Fatalf("artifact: %d %q", resp.StatusCode, body[:n])
	}

	// whitelist: only run artifacts are served (the stdlib client
	// normalizes ".." away, so a forbidden plain name is the real check)
	resp, err = http.Get(srv.URL + "/runs/" + job.ID + "/verdict.db")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-artifact file: %d, want 403", resp.StatusCode)
	}

	// unknown id
	resp, err = http.Get(srv.URL + "/runs/999")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("unknown id: %d, want 404", resp.StatusCode)
	}
}

func TestListRuns(t *testing.T) {
	handler, drain := New(func(_ context.Context, _ pipeline.Options) (*pipeline.State, string, error) {
		return nil, t.TempDir(), nil
	})
	defer drain()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/runs", "application/json",
		stringsReader(`{"dois":["10.1000/a"],"topic":"t"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	time.Sleep(300 * time.Millisecond) // let the worker finish

	resp, err = http.Get(srv.URL + "/runs")
	if err != nil {
		t.Fatal(err)
	}
	var jobs []Job
	_ = json.NewDecoder(resp.Body).Decode(&jobs)
	resp.Body.Close()
	if len(jobs) != 1 || jobs[0].Topic != "t" {
		t.Fatalf("list: %+v", jobs)
	}
}

func stringsReader(s string) io.Reader { return strings.NewReader(s) }
