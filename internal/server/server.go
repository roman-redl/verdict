// Package server is verdictd: an HTTP wrapper over the pipeline. Submit
// runs (question or DOIs), watch status, fetch artifacts. One worker
// executes one run at a time — the LLM concurrency budget lives inside the
// pipeline stages.
//
// Jobs are stored in the Jobs port (SQLite in production) and survive a
// restart. A job that was queued or running when the daemon stopped is marked
// interrupted at the next start rather than re-queued: a run spends paid
// quota (Consensus, the LLM), and a restart must never silently spend it
// again. The run directory keeps the finished stages, so the CLI can continue
// it with --resume.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/roman-redl/verdict/internal/pipeline"
)

const (
	// queueSize is how many submitted jobs may wait for the worker.
	queueSize = 100
	// storeTimeout bounds one job-table write that must succeed even while
	// the daemon is shutting down.
	storeTimeout = 10 * time.Second
	// interruptedReason is what a client reads about a job the daemon lost.
	interruptedReason = "the daemon stopped before the job finished; submit it again, " +
		"or continue the run directory with `verdict run --run-dir <dir> --resume`"
)

// Runner executes one pipeline run (injected; cmd/verdictd wires the real
// pipeline, tests wire a fake).
type Runner func(ctx context.Context, opts pipeline.Options) (*pipeline.State, string, error)

type queuedJob struct {
	id   string
	opts pipeline.Options
}

// Server holds the queue and the worker. The HTTP side is Handler; the worker
// is Work, which the caller runs until its context ends.
type Server struct {
	log     *slog.Logger
	run     Runner
	jobs    Jobs
	runsDir string // where uploaded DOI lists land
	queue   chan queuedJob
}

// New builds a server and settles the leftovers of the previous process: jobs
// that were queued or running are marked interrupted. runsDir is the directory
// for uploaded DOI lists.
func New(ctx context.Context, run Runner, jobs Jobs, runsDir string, log *slog.Logger) (*Server, error) {
	switch {
	case run == nil:
		return nil, errors.New("the server needs a runner")
	case jobs == nil:
		return nil, errors.New("the server needs a job store")
	case log == nil:
		return nil, errors.New("the server needs a logger")
	}

	n, err := jobs.InterruptUnfinished(ctx, interruptedReason)
	if err != nil {
		return nil, fmt.Errorf("settling the jobs of the previous run: %w", err)
	}

	if n > 0 {
		log.Warn("jobs left by the previous process marked interrupted", "jobs", n)
	}

	return &Server{log: log, run: run, jobs: jobs, runsDir: runsDir, queue: make(chan queuedJob, queueSize)}, nil
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
	log := s.log.With("job", q.id)

	if err := s.jobs.Start(ctx, q.id); err != nil {
		// not queued any more (or the store failed): nothing to run
		log.Error("job not started", "err", err)
		return
	}

	st, dir, err := s.run(ctx, q.opts)

	// the outcome is stored even when ctx is already canceled by a shutdown
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()

	if ferr := s.jobs.Finish(sctx, q.id, outcome(ctx, log, st, dir, err)); ferr != nil {
		log.Error("job outcome not stored", "err", ferr)
	}
}

// outcome maps the end of a run onto the job record.
func outcome(ctx context.Context, log *slog.Logger, st *pipeline.State, dir string, err error) Outcome {
	switch {
	case err != nil && ctx.Err() != nil:
		log.Warn("job interrupted by shutdown", "err", err)
		return Outcome{Status: JobInterrupted, Dir: dir, Error: interruptedReason}
	case err != nil:
		log.Error("job failed", "err", err)
		return Outcome{Status: JobFailed, Error: userMessage(err)}
	case st != nil && st.Report != nil:
		return Outcome{Status: JobDone, Dir: dir, Verdict: st.Report.Verdict}
	}

	return Outcome{Status: JobDone, Dir: dir}
}

// userMessage is what a client may read about a failed run: the text of a
// request error, otherwise a generic line (SQL, paths and upstream API
// answers stay in the daemon log).
func userMessage(err error) string {
	if ue, ok := errors.AsType[*pipeline.UserError](err); ok {
		return ue.Msg
	}

	return "the run failed; details are in the daemon log"
}
