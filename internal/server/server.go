// Package server is verdictd: an HTTP wrapper over the pipeline. Submit
// runs (question or DOIs), watch status, fetch artifacts. One worker
// executes one run at a time — the LLM concurrency budget lives inside the
// pipeline stages.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/roman-redl/verdict/internal/pipeline"
)

// maxSubmitBytes caps a POST /runs body: a question or a DOI list is a few
// kilobytes, anything near the cap is a mistake or abuse.
const maxSubmitBytes = 1 << 20

// Job statuses.
const (
	statusQueued      = "queued"
	statusRunning     = "running"
	statusDone        = "done"
	statusFailed      = "failed"
	statusInterrupted = "interrupted"
)

// Runner executes one pipeline run (injected; cmd/verdictd wires the real
// pipeline, tests wire a fake).
type Runner func(ctx context.Context, opts pipeline.Options) (*pipeline.State, string, error)

// Job is a submitted run's lifecycle.
type Job struct {
	ID       string `json:"id"`
	Status   string `json:"status"` // queued | running | done | failed | interrupted
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

// Server holds the job table, the queue and the worker. The HTTP side is
// Handler; the worker is Work, which the caller runs until its context ends.
type Server struct {
	log     *slog.Logger
	run     Runner
	runsDir string // where uploaded DOI lists land

	mu     sync.Mutex
	nextID int
	jobs   map[string]*Job
	queue  chan queuedJob
}

// New builds a server. runsDir is the directory for uploaded DOI lists.
func New(run Runner, runsDir string, log *slog.Logger) (*Server, error) {
	if run == nil {
		return nil, errors.New("server needs a runner")
	}

	if log == nil {
		return nil, errors.New("server needs a logger")
	}

	return &Server{
		log:     log,
		run:     run,
		runsDir: runsDir,
		jobs:    map[string]*Job{},
		queue:   make(chan queuedJob, 100),
	}, nil
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /runs", s.submit)
	mux.HandleFunc("GET /runs", s.list)
	mux.HandleFunc("GET /runs/{id}", s.get)
	mux.HandleFunc("GET /runs/{id}/{file...}", s.artifact)

	return mux
}

// Work runs queued jobs one at a time until ctx is canceled. The job context
// derives from ctx, so a shutdown signal interrupts the running pipeline run
// instead of waiting for it to finish; such a job ends as interrupted.
func (s *Server) Work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case q := <-s.queue:
			s.execute(ctx, q)
		}
	}
}

func (s *Server) execute(ctx context.Context, q queuedJob) {
	id := q.job.ID
	log := s.log.With("job", id)

	s.update(id, func(j *Job) { j.Status = statusRunning })

	st, dir, err := s.run(ctx, q.opts)

	switch {
	case err != nil && ctx.Err() != nil:
		log.Warn("job interrupted by shutdown", "err", err)
		s.update(id, func(j *Job) { j.Status, j.Dir = statusInterrupted, dir })
	case err != nil:
		log.Error("job failed", "err", err)
		s.update(id, func(j *Job) { j.Status, j.Error = statusFailed, err.Error() })
	case st != nil && st.Report != nil:
		s.update(id, func(j *Job) { j.Status, j.Dir, j.Verdict = statusDone, dir, st.Report.Verdict })
	default:
		s.update(id, func(j *Job) { j.Status, j.Dir = statusDone, dir })
	}
}

func (s *Server) update(id string, fn func(*Job)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if j, ok := s.jobs[id]; ok {
		fn(j)
	}
}

// snapshot returns a copy of the job taken under the mutex: the worker
// mutates job fields concurrently, encoding or reading a *Job outside the
// lock is a data race (torn JSON in responses).
func (s *Server) snapshot(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}

	return *j, true
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question  string   `json:"question"`
		DOIs      []string `json:"dois"`
		Topic     string   `json:"topic"`
		MaxPapers int      `json:"max_papers"`
		TextMode  string   `json:"text_mode"`  // lean | hybrid (default) | full
		StopAfter string   `json:"stop_after"` // e.g. "2" — deterministic stages only
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxSubmitBytes)

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}

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
		TextMode:  req.TextMode,
		StopAfter: req.StopAfter,
	}

	if len(req.DOIs) > 0 {
		file, err := s.writeDOIList(req.DOIs)
		if err != nil {
			s.log.Error("saving the DOI list failed", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		opts.DOIFile = file
	}

	if opts.Question == "" {
		opts.Question = "Evidence assessment over a given DOI list"
	}

	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("%d", s.nextID)
	job := &Job{ID: id, Status: statusQueued, Question: opts.Question, Topic: req.Topic}
	s.jobs[id] = job
	s.mu.Unlock()

	select {
	case s.queue <- queuedJob{job: job, opts: opts}:
	default:
		s.update(id, func(j *Job) { j.Status, j.Error = statusFailed, "queue full" })
	}

	// the copy is required: the worker may have started already
	snap, _ := s.snapshot(id)

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(snap)
}

// writeDOIList stores the submitted DOIs where the pipeline reads them
// (Options.DOIFile) and returns the file path.
func (s *Server) writeDOIList(dois []string) (string, error) {
	if err := os.MkdirAll(s.runsDir, 0o750); err != nil {
		return "", fmt.Errorf("creating %s: %w", s.runsDir, err)
	}

	f, err := os.CreateTemp(s.runsDir, "dois-*.txt")
	if err != nil {
		return "", fmt.Errorf("creating the DOI list: %w", err)
	}

	for _, d := range dois {
		if _, err := fmt.Fprintln(f, d); err != nil {
			_ = f.Close()
			return "", fmt.Errorf("writing %s: %w", f.Name(), err)
		}
	}

	if err := f.Close(); err != nil {
		return "", fmt.Errorf("closing %s: %w", f.Name(), err)
	}

	return f.Name(), nil
}

func (s *Server) list(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}

	writeJSON(w, out)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	j, ok := s.snapshot(r.PathValue("id"))
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

func (s *Server) artifact(w http.ResponseWriter, r *http.Request) {
	j, ok := s.snapshot(r.PathValue("id"))
	if !ok || j.Dir == "" {
		http.NotFound(w, r)
		return
	}

	name := filepath.Base(r.PathValue("file"))
	if !artifactWhitelist.MatchString(name) {
		http.Error(w, "artifact not served", http.StatusForbidden)
		return
	}
	//nolint:gosec // the file name passed the artifact whitelist, the directory is the service's own state
	b, err := os.ReadFile(filepath.Join(j.Dir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	//nolint:gosec // the response is a run artifact read from disk; the name passed the whitelist
	_, _ = w.Write(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
