package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/server"
)

func fixedClock() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC) }

func TestJobLifecycle(t *testing.T) {
	jobs := openTemp(t).Jobs(fixedClock)
	ctx := t.Context()

	job, err := jobs.Create(ctx, server.NewJob{Question: questionWork, Topic: topicCreatine})
	require.NoError(t, err)
	assert.Equal(t, server.Job{ID: "1", Status: server.JobQueued, Question: questionWork, Topic: topicCreatine}, job)

	require.NoError(t, jobs.Start(ctx, job.ID))

	got, err := jobs.Get(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, server.JobRunning, got.Status)

	done := server.Outcome{Status: server.JobDone, Dir: "runs/t/r", Verdict: verdictWeak}
	require.NoError(t, jobs.Finish(ctx, job.ID, done))

	got, err = jobs.Get(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, server.Job{
		ID: "1", Status: server.JobDone, Question: questionWork, Topic: topicCreatine,
		Dir: "runs/t/r", Verdict: verdictWeak,
	}, got)
}

// Transitions are conditional: a stale or repeated call is a conflict, not a
// silent overwrite of a finished job.
func TestJobTransitionsAreConditional(t *testing.T) {
	jobs := openTemp(t).Jobs(fixedClock)
	ctx := t.Context()

	job, err := jobs.Create(ctx, server.NewJob{Question: "q"})
	require.NoError(t, err)

	require.NoError(t, jobs.Start(ctx, job.ID))
	require.ErrorIs(t, jobs.Start(ctx, job.ID), server.ErrConflict, "already running")

	require.NoError(t, jobs.Finish(ctx, job.ID, server.Outcome{Status: server.JobFailed, Error: "boom"}))
	require.ErrorIs(t, jobs.Finish(ctx, job.ID, server.Outcome{Status: server.JobDone}), server.ErrConflict,
		"a finished job is never overwritten")
	require.ErrorIs(t, jobs.Start(ctx, job.ID), server.ErrConflict, "a finished job does not start")

	got, err := jobs.Get(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, server.JobFailed, got.Status)
	assert.Equal(t, "boom", got.Error)

	require.ErrorIs(t, jobs.Start(ctx, "999"), server.ErrConflict, "no such job to start")
}

// A queued job can fail without ever running (the queue was full).
func TestJobFinishFromQueued(t *testing.T) {
	jobs := openTemp(t).Jobs(fixedClock)
	ctx := t.Context()

	job, err := jobs.Create(ctx, server.NewJob{Question: "q"})
	require.NoError(t, err)

	require.NoError(t, jobs.Finish(ctx, job.ID, server.Outcome{Status: server.JobFailed, Error: "queue full"}))
}

func TestJobGetUnknown(t *testing.T) {
	jobs := openTemp(t).Jobs(fixedClock)

	for _, id := range []string{"42", "not-a-number", ""} {
		_, err := jobs.Get(t.Context(), id)
		require.ErrorIs(t, err, server.ErrNotFound, id)
	}
}

func TestJobListIsOrdered(t *testing.T) {
	jobs := openTemp(t).Jobs(fixedClock)
	ctx := t.Context()

	got, err := jobs.List(ctx)
	require.NoError(t, err)
	require.NotNil(t, got, "an empty table lists as [] in JSON, not null")
	require.Empty(t, got)

	for _, q := range []string{"a", "b", "c"} {
		_, err := jobs.Create(ctx, server.NewJob{Question: q})
		require.NoError(t, err)
	}

	got, err = jobs.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"1", "2", "3"}, []string{got[0].ID, got[1].ID, got[2].ID})
	assert.Equal(t, []string{"a", "b", "c"}, []string{got[0].Question, got[1].Question, got[2].Question})
}

// What a stopped daemon leaves behind is settled; finished jobs are not touched.
func TestJobInterruptUnfinished(t *testing.T) {
	jobs := openTemp(t).Jobs(fixedClock)
	ctx := t.Context()

	create := func(q string) string {
		j, err := jobs.Create(ctx, server.NewJob{Question: q})
		require.NoError(t, err)

		return j.ID
	}

	done, running, queued := create("done"), create("running"), create("queued")

	require.NoError(t, jobs.Start(ctx, done))
	require.NoError(t, jobs.Finish(ctx, done, server.Outcome{Status: server.JobDone, Verdict: "strong"}))
	require.NoError(t, jobs.Start(ctx, running))

	n, err := jobs.InterruptUnfinished(ctx, "daemon stopped")
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	for id, want := range map[string]server.JobStatus{
		done: server.JobDone, running: server.JobInterrupted, queued: server.JobInterrupted,
	} {
		j, err := jobs.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, want, j.Status, j.Question)
	}

	j, err := jobs.Get(ctx, running)
	require.NoError(t, err)
	assert.Equal(t, "daemon stopped", j.Error)

	n, err = jobs.InterruptUnfinished(ctx, "again")
	require.NoError(t, err)
	assert.Zero(t, n, "nothing is left to interrupt")
}

// The table lives in the database file: it survives a reopen, ids keep
// counting, and the timestamps come from the injected clock.
func TestJobsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdict.db")
	ctx := t.Context()

	d, err := Open(ctx, path)
	require.NoError(t, err)

	first, err := d.Jobs(fixedClock).Create(ctx, server.NewJob{Question: "before the restart"})
	require.NoError(t, err)
	require.NoError(t, d.Close())

	d, err = Open(ctx, path)
	require.NoError(t, err)

	defer func() { require.NoError(t, d.Close()) }()

	jobs := d.Jobs(fixedClock)

	got, err := jobs.Get(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "before the restart", got.Question)

	second, err := jobs.Create(ctx, server.NewJob{Question: "after"})
	require.NoError(t, err)
	assert.Equal(t, "2", second.ID, "ids keep counting across restarts")

	var created string
	require.NoError(t, d.sql.QueryRowContext(ctx, `SELECT created_at FROM jobs WHERE id = 1`).Scan(&created))
	assert.Equal(t, "2026-11-01T12:00:00Z", created)
}

// An index from before the job table (schema version 1) gains it in place.
func TestOpenAddsJobsToAnExistingIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdict.db")
	ctx := t.Context()

	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)

	_, err = old.ExecContext(ctx, `CREATE TABLE runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT, topic TEXT NOT NULL DEFAULT '', run TEXT NOT NULL,
		dir TEXT NOT NULL UNIQUE, question TEXT NOT NULL DEFAULT '', verdict TEXT NOT NULL DEFAULT '',
		consensus TEXT NOT NULL DEFAULT '', papers INTEGER NOT NULL DEFAULT 0,
		generated_at TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT (datetime('now')))`)
	require.NoError(t, err)
	_, err = old.ExecContext(ctx, `PRAGMA user_version = 1`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	d := func() *DB {
		d, err := Open(ctx, path)
		require.NoError(t, err)

		return d
	}()

	defer func() { require.NoError(t, d.Close()) }()

	_, err = d.Jobs(fixedClock).Create(ctx, server.NewJob{Question: "q"})
	require.NoError(t, err)
	assert.Equal(t, latestVersion(t), schemaVersion(t, d))
}
