package server

import (
	"context"
	"errors"
)

// JobStatus is a job's place in its lifecycle.
type JobStatus string

const (
	JobQueued      JobStatus = "queued"
	JobRunning     JobStatus = "running"
	JobDone        JobStatus = "done"
	JobFailed      JobStatus = "failed"
	JobInterrupted JobStatus = "interrupted"
)

// Job is a submitted run's lifecycle.
type Job struct {
	ID       string    `json:"id"`
	Status   JobStatus `json:"status"`
	Question string    `json:"question"`
	Topic    string    `json:"topic,omitempty"`
	Dir      string    `json:"dir,omitempty"`
	Verdict  string    `json:"verdict,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// NewJob is what a submit records.
type NewJob struct {
	Question string
	Topic    string
}

// Outcome is how a job ended.
type Outcome struct {
	Status  JobStatus // done, failed or interrupted
	Dir     string
	Verdict string
	Error   string // text for the client, never a raw error
}

var (
	// ErrNotFound: no job has that ID.
	ErrNotFound = errors.New("job not found")
	// ErrConflict: the job is not in a state the transition starts from.
	ErrConflict = errors.New("job is not in the expected state")
)

// Jobs is the job table. Jobs survive a restart, so a client can still read
// the outcome of a run after the daemon was stopped; transitions are
// conditional, so a stale or repeated call is a conflict, not a silent
// overwrite.
type Jobs interface {
	// Create records a queued job and returns it with its ID.
	Create(ctx context.Context, j NewJob) (Job, error)
	// Start moves a queued job to running (ErrConflict otherwise).
	Start(ctx context.Context, id string) error
	// Finish ends a queued or running job (ErrConflict otherwise).
	Finish(ctx context.Context, id string, o Outcome) error
	// Get returns a job (ErrNotFound).
	Get(ctx context.Context, id string) (Job, error)
	// List returns all jobs, oldest first.
	List(ctx context.Context) ([]Job, error)
	// InterruptUnfinished marks every queued or running job interrupted, with
	// the reason as its error text — the leftovers of a stopped daemon — and
	// reports how many there were.
	InterruptUnfinished(ctx context.Context, reason string) (int, error)
}
