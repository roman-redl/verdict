package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/roman-redl/verdict/internal/server"
)

var _ server.Jobs = (*JobStore)(nil)

// JobStore keeps the daemon's jobs in the same SQLite file as the run index.
type JobStore struct {
	db  *DB
	now func() time.Time
}

// Jobs returns the job table of the database; now stamps the rows.
func (db *DB) Jobs(now func() time.Time) *JobStore {
	return &JobStore{db: db, now: now}
}

// jobCols is the column list the job queries read, in Scan order.
const jobCols = "id, status, question, topic, dir, verdict, error"

// stamp formats a row timestamp.
func (s *JobStore) stamp() string { return s.now().UTC().Format(time.RFC3339) }

// Create records a queued job.
func (s *JobStore) Create(ctx context.Context, j server.NewJob) (server.Job, error) {
	stamp := s.stamp()

	res, err := s.db.sql.ExecContext(ctx, `
		INSERT INTO jobs (status, question, topic, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, server.JobQueued, j.Question, j.Topic, stamp, stamp)
	if err != nil {
		return server.Job{}, fmt.Errorf("creating a job: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return server.Job{}, fmt.Errorf("reading the new job's id: %w", err)
	}

	return server.Job{
		ID: strconv.FormatInt(id, 10), Status: server.JobQueued, Question: j.Question, Topic: j.Topic,
	}, nil
}

// Start moves a queued job to running.
func (s *JobStore) Start(ctx context.Context, id string) error {
	res, err := s.db.sql.ExecContext(ctx, `
		UPDATE jobs
		SET status = ?, updated_at = ?
		WHERE id = ? AND status = ?
	`, server.JobRunning, s.stamp(), id, server.JobQueued)

	return s.transitioned(res, err, "starting job "+id)
}

// Finish ends a queued or running job.
func (s *JobStore) Finish(ctx context.Context, id string, o server.Outcome) error {
	res, err := s.db.sql.ExecContext(ctx, `
		UPDATE jobs
		SET status = ?, dir = ?, verdict = ?, error = ?, updated_at = ?
		WHERE id = ? AND status IN (?, ?)
	`, o.Status, o.Dir, o.Verdict, o.Error, s.stamp(), id, server.JobQueued, server.JobRunning)

	return s.transitioned(res, err, "finishing job "+id)
}

// transitioned turns the result of a conditional UPDATE into an error: no
// matching row means the job is gone or was not in the expected state.
func (s *JobStore) transitioned(res sql.Result, err error, what string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}

	if n == 0 {
		return fmt.Errorf("%s: %w", what, server.ErrConflict)
	}

	return nil
}

// Get returns one job.
func (s *JobStore) Get(ctx context.Context, id string) (server.Job, error) {
	var j server.Job

	err := s.db.sql.QueryRowContext(ctx, `
		SELECT `+jobCols+`
		FROM jobs
		WHERE id = ?
	`, id).Scan(&j.ID, &j.Status, &j.Question, &j.Topic, &j.Dir, &j.Verdict, &j.Error)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return server.Job{}, fmt.Errorf("job %s: %w", id, server.ErrNotFound)
	case err != nil:
		return server.Job{}, fmt.Errorf("reading job %s: %w", id, err)
	}

	return j, nil
}

// List returns all jobs, oldest first.
func (s *JobStore) List(ctx context.Context) ([]server.Job, error) {
	rows, err := s.db.sql.QueryContext(ctx, `
		SELECT `+jobCols+`
		FROM jobs
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}

	defer func() { _ = rows.Close() }() // read-only cursor: a close failure loses nothing

	jobs := []server.Job{}

	for rows.Next() {
		var j server.Job
		if err := rows.Scan(&j.ID, &j.Status, &j.Question, &j.Topic, &j.Dir, &j.Verdict, &j.Error); err != nil {
			return nil, fmt.Errorf("reading a job row: %w", err)
		}

		jobs = append(jobs, j)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading jobs: %w", err)
	}

	return jobs, nil
}

// InterruptUnfinished marks every queued or running job interrupted.
func (s *JobStore) InterruptUnfinished(ctx context.Context, reason string) (int, error) {
	res, err := s.db.sql.ExecContext(ctx, `
		UPDATE jobs
		SET status = ?, error = ?, updated_at = ?
		WHERE status IN (?, ?)
	`, server.JobInterrupted, reason, s.stamp(), server.JobQueued, server.JobRunning)
	if err != nil {
		return 0, fmt.Errorf("interrupting unfinished jobs: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting interrupted jobs: %w", err)
	}

	return int(n), nil
}
