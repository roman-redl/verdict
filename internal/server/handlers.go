package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/roman-redl/verdict/internal/pipeline"
)

// maxSubmitBytes caps a POST /runs body: a question or a DOI list is a few
// kilobytes, anything near the cap is a mistake or abuse.
const maxSubmitBytes = 1 << 20

// defaultDOIListQuestion titles a job submitted with DOIs only.
const defaultDOIListQuestion = "Evidence assessment over a given DOI list"

// submitRequest is the body of POST /runs.
type submitRequest struct {
	Question  string   `json:"question"`
	DOIs      []string `json:"dois"`
	Topic     string   `json:"topic"`
	MaxPapers int      `json:"max_papers"`
	TextMode  string   `json:"text_mode"`  // lean | hybrid (default) | full
	StopAfter string   `json:"stop_after"` // e.g. "2" — deterministic stages only
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSubmitBytes)

	var req submitRequest
	if !s.decode(w, r, &req) {
		return
	}

	opts, ok := s.options(w, &req)
	if !ok {
		return
	}

	job, err := s.jobs.Create(r.Context(), NewJob{Question: opts.Question, Topic: req.Topic})
	if err != nil {
		s.fail(w, "recording the job failed", err)
		return
	}

	select {
	case s.queue <- queuedJob{id: job.ID, opts: opts}:
	default:
		s.rejectFull(r.Context(), job.ID)
	}

	// the stored job, not the one just created: the worker may have started it
	job, err = s.jobs.Get(r.Context(), job.ID)
	if err != nil {
		s.fail(w, "reading the job back failed", err)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	s.writeJSON(w, job)
}

// decode reads the JSON body, answering the client itself when it is bad.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return false
		}

		s.log.Warn("rejected a malformed request body", "err", err)
		http.Error(w, "the request body is not valid JSON", http.StatusBadRequest)

		return false
	}

	return true
}

// options validates a request and turns it into pipeline options, storing an
// uploaded DOI list on disk. It answers the client itself when it refuses.
func (s *Server) options(w http.ResponseWriter, req *submitRequest) (pipeline.Options, bool) {
	if req.Question == "" && len(req.DOIs) == 0 {
		http.Error(w, `"question" or "dois" is required`, http.StatusBadRequest)
		return pipeline.Options{}, false
	}

	mode, err := pipeline.ParseTextMode(req.TextMode)
	if err != nil {
		http.Error(w, userMessage(err), http.StatusBadRequest)
		return pipeline.Options{}, false
	}

	opts := pipeline.Options{
		Question:  req.Question,
		Topic:     req.Topic,
		MaxPapers: req.MaxPapers,
		TextMode:  mode,
		StopAfter: req.StopAfter,
	}

	if len(req.DOIs) > 0 {
		file, err := s.writeDOIList(req.DOIs)
		if err != nil {
			s.fail(w, "saving the DOI list failed", err)
			return pipeline.Options{}, false
		}

		opts.DOIFile = file
	}

	if opts.Question == "" {
		opts.Question = defaultDOIListQuestion
	}

	return opts, true
}

// rejectFull ends a job that found the queue full.
func (s *Server) rejectFull(ctx context.Context, id string) {
	err := s.jobs.Finish(ctx, id, Outcome{Status: JobFailed, Error: "queue full"})
	if err != nil {
		s.log.Error("could not mark a rejected job failed", "job", id, "err", err)
	}
}

// fail answers 500 with a generic text and logs the cause.
func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	s.log.Error(what, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
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
			_ = f.Close() // the write error below is the one that matters

			return "", fmt.Errorf("writing %s: %w", f.Name(), err)
		}
	}

	if err := f.Close(); err != nil {
		return "", fmt.Errorf("closing %s: %w", f.Name(), err)
	}

	return f.Name(), nil
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.jobs.List(r.Context())
	if err != nil {
		s.fail(w, "listing the jobs failed", err)
		return
	}

	s.writeJSON(w, jobs)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	job, ok := s.lookup(w, r)
	if !ok {
		return
	}

	s.writeJSON(w, job)
}

// lookup finds the job named in the path, answering 404 or 500 itself.
func (s *Server) lookup(w http.ResponseWriter, r *http.Request) (Job, bool) {
	job, err := s.jobs.Get(r.Context(), r.PathValue("id"))

	switch {
	case errors.Is(err, ErrNotFound):
		http.NotFound(w, r)
		return Job{}, false
	case err != nil:
		s.fail(w, "reading the job failed", err)
		return Job{}, false
	}

	return job, true
}

// artifactWhitelist guards the artifact endpoint against path traversal:
// stage JSON, the reports, the input trail and the vision notes.
var artifactWhitelist = regexp.MustCompile(
	`^([1-4]-[a-z]+\.json|report\.md|matrix\.csv|context\.md|0-input\.txt|3b-vision\.json)$`)

func (s *Server) artifact(w http.ResponseWriter, r *http.Request) {
	job, ok := s.lookup(w, r)
	if !ok {
		return
	}

	if job.Dir == "" {
		http.NotFound(w, r)
		return
	}

	name := filepath.Base(r.PathValue("file"))
	if !artifactWhitelist.MatchString(name) {
		http.Error(w, "artifact not served", http.StatusForbidden)
		return
	}
	//nolint:gosec // the file name passed the artifact whitelist, the directory is the service's own state
	b, err := os.ReadFile(filepath.Join(job.Dir, name))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Error("reading an artifact failed", "job", job.ID, "file", name, "err", err)
		}

		http.NotFound(w, r)

		return
	}
	//nolint:gosec // the response is a run artifact read from disk; the name passed the whitelist
	if _, err := w.Write(b); err != nil {
		s.log.Warn("writing the artifact response failed", "job", job.ID, "file", name, "err", err)
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")

	// a failure here is a client that went away mid-response
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.log.Warn("writing the JSON response failed", "err", err)
	}
}
