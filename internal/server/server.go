// Package server is verdictd: an HTTP wrapper over the pipeline. Submit
// runs (question or DOIs), watch status, fetch artifacts. One worker
// executes one run at a time — the LLM concurrency budget lives inside the
// pipeline stages.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/roman-redl/verdict/internal/pipeline"
)

// Runner executes one pipeline run (injected; cmd/verdictd wires the real
// pipeline, tests wire a fake).
type Runner func(ctx context.Context, opts pipeline.Options) (*pipeline.State, string, error)

// Job is a submitted run's lifecycle.
type Job struct {
	ID       string `json:"id"`
	Status   string `json:"status"` // queued | running | done | failed
	Question string `json:"question"`
	Topic    string `json:"topic,omitempty"`
	Dir      string `json:"dir,omitempty"`
	Verdict  string `json:"verdict,omitempty"`
	Error    string `json:"error,omitempty"`
}

type queuedJob struct {
	job  *Job
	opts pipeline.Options
}

type service struct {
	mu     sync.Mutex
	nextID int
	jobs   map[string]*Job
	queue  chan queuedJob
	run    Runner
	wg     sync.WaitGroup
}

// New returns the HTTP handler and a drain function (call it on shutdown:
// it stops taking jobs and waits for the running one to finish).
func New(run Runner) (http.Handler, func()) {
	s := &service{
		jobs:  map[string]*Job{},
		queue: make(chan queuedJob, 100),
		run:   run,
	}
	s.wg.Add(1)
	go s.worker()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /runs", s.submit)
	mux.HandleFunc("GET /runs", s.list)
	mux.HandleFunc("GET /runs/{id}", s.get)
	mux.HandleFunc("GET /runs/{id}/{file...}", s.artifact)
	return mux, func() {
		close(s.queue)
		s.wg.Wait()
	}
}

func (s *service) worker() {
	defer s.wg.Done()
	for q := range s.queue {
		s.update(q.job.ID, func(j *Job) { j.Status = "running" })
		st, dir, err := s.run(context.Background(), q.opts)
		s.update(q.job.ID, func(j *Job) {
			switch {
			case err != nil:
				j.Status, j.Error = "failed", err.Error()
			case st != nil && st.Report != nil:
				j.Status, j.Dir, j.Verdict = "done", dir, st.Report.Verdict
			default:
				j.Status, j.Dir = "done", dir
			}
		})
	}
}

func (s *service) update(id string, fn func(*Job)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		fn(j)
	}
}

func (s *service) submit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question  string   `json:"question"`
		DOIs      []string `json:"dois"`
		Topic     string   `json:"topic"`
		MaxPapers int      `json:"max_papers"`
		FullText  bool     `json:"fulltext"`
		StopAfter string   `json:"stop_after"` // e.g. "2" — deterministic stages only
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Question == "" && len(req.DOIs) == 0 {
		http.Error(w, `"question" or "dois" is required`, http.StatusBadRequest)
		return
	}
	opts := pipeline.Options{
		Question:  req.Question,
		Topic:     req.Topic,
		MaxPapers: req.MaxPapers,
		FullText:  req.FullText,
		StopAfter: req.StopAfter,
	}
	if len(req.DOIs) > 0 {
		if err := os.MkdirAll("runs", 0o755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		f, err := os.CreateTemp("runs", "dois-*.txt")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, d := range req.DOIs {
			fmt.Fprintln(f, d)
		}
		f.Close()
		opts.DOIFile = f.Name()
	}

	if opts.Question == "" {
		opts.Question = "Evidence assessment over a given DOI list"
	}
	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("%d", s.nextID)
	job := &Job{ID: id, Status: "queued", Question: opts.Question, Topic: req.Topic}
	s.jobs[id] = job
	s.mu.Unlock()

	select {
	case s.queue <- queuedJob{job: job, opts: opts}:
	default:
		s.update(id, func(j *Job) { j.Status, j.Error = "failed", "queue full" })
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(s.jobs[id])
}

func (s *service) list(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	writeJSON(w, out)
}

func (s *service) get(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	j, ok := s.jobs[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, j)
}

// artifactWhitelist guards the artifact endpoint against path traversal:
// stage JSON, the reports, the input trail and the vision notes.
var artifactWhitelist = regexp.MustCompile(
	`^([1-4]-[a-z]+\.json|report\.md|matrix\.csv|context\.md|0-input\.txt|3b-vision\.json)$`)

func (s *service) artifact(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	j, ok := s.jobs[r.PathValue("id")]
	s.mu.Unlock()
	if !ok || j.Dir == "" {
		http.NotFound(w, r)
		return
	}
	name := filepath.Base(r.PathValue("file"))
	if !artifactWhitelist.MatchString(name) {
		http.Error(w, "artifact not served", http.StatusForbidden)
		return
	}
	b, err := os.ReadFile(filepath.Join(j.Dir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Write(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
