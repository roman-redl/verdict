package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// Fixture values repeated across the tests.
const (
	topicCreatine = "creatine"
	verdictWeak   = "weak"
	questionWork  = "does it work"
)

// stamp formats an index timestamp the way the CLI does.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func openTemp(t *testing.T) *DB {
	t.Helper()

	d, err := Open(t.Context(), filepath.Join(t.TempDir(), "verdict.db"))
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, d.Close()) })

	return d
}

func TestRecordAndRuns(t *testing.T) {
	d := openTemp(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	base := Row{
		Topic: "keratosis", Run: "kp-light-full", Dir: "runs/keratosis/kp-light-full",
		Question: "does light therapy work", Verdict: "moderate", Consensus: "broad_agreement",
		Papers: 10, GeneratedAt: stamp(now),
	}
	require.NoError(t, d.Record(ctx, base))

	// a re-record of the same dir is an update, not a duplicate
	base.Verdict = verdictWeak
	require.NoError(t, d.Record(ctx, base))

	require.NoError(t, d.Record(ctx, Row{
		Topic: topicCreatine, Run: "r2", Dir: "runs/creatine/r2",
		Verdict: "strong", GeneratedAt: stamp(now),
	}))

	all, err := d.Runs(ctx, "")
	require.NoError(t, err)

	require.Len(t, all, 2)
	require.Equal(t, topicCreatine, all[0].Topic, "newest first")

	one, err := d.Runs(ctx, "keratosis")
	require.NoError(t, err)

	require.Len(t, one, 1, "topic filter")
	require.Equal(t, verdictWeak, one[0].Verdict, "upsert")
	require.Equal(t, 10, one[0].Papers, "upsert")
}

// The runs index created by the pre-migration code (CREATE TABLE IF NOT
// EXISTS, no user_version) must open in place: its rows survive and the
// database is adopted at version 1 rather than failing on "table exists".
func TestOpenAdoptsDatabaseFromBeforeMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdict.db")
	ctx := t.Context()

	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)

	// the schema as the previous code created it, verbatim
	_, err = old.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS runs (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		topic       TEXT NOT NULL DEFAULT '',
		run         TEXT NOT NULL,
		dir         TEXT NOT NULL UNIQUE,
		question    TEXT NOT NULL DEFAULT '',
		verdict     TEXT NOT NULL DEFAULT '',
		consensus   TEXT NOT NULL DEFAULT '',
		papers      INTEGER NOT NULL DEFAULT 0,
		generated_at TEXT NOT NULL DEFAULT '',
		created_at  TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	require.NoError(t, err)

	_, err = old.ExecContext(ctx, `INSERT INTO runs (topic, run, dir, question, verdict, consensus, papers, generated_at)
		VALUES ('creatine', 'r1', 'runs/creatine/r1', 'q', 'moderate', 'contested', 12, '2026-10-01T10:00:00Z')`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	d, err := Open(ctx, path)
	require.NoError(t, err)

	defer func() { require.NoError(t, d.Close()) }()

	rows, err := d.Runs(ctx, "")
	require.NoError(t, err)
	require.Len(t, rows, 1, "the old index rows must survive")
	require.Equal(t, "runs/creatine/r1", rows[0].Dir)
	require.Equal(t, 12, rows[0].Papers)

	require.NoError(t, d.Record(ctx, Row{Topic: topicCreatine, Run: "r2", Dir: "runs/creatine/r2"}))
	require.Equal(t, latestVersion(t), schemaVersion(t, d), "the database must be adopted at the latest version")
}

// Reopening an already migrated database changes nothing.
func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdict.db")
	ctx := t.Context()

	d, err := Open(ctx, path)
	require.NoError(t, err)
	require.NoError(t, d.Record(ctx, Row{Run: "r", Dir: "runs/r"}))
	require.NoError(t, d.Close())

	d, err = Open(ctx, path)
	require.NoError(t, err)

	defer func() { require.NoError(t, d.Close()) }()

	rows, err := d.Runs(ctx, "")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, latestVersion(t), schemaVersion(t, d))
}

// The connection settings are explicit, not the driver defaults.
func TestOpenSetsConnectionPragmas(t *testing.T) {
	d := openTemp(t)
	ctx := t.Context()

	var journal string
	require.NoError(t, d.sql.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal))
	require.Equal(t, "wal", journal)

	var busy, fk int
	require.NoError(t, d.sql.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busy))
	require.Equal(t, 5000, busy)
	require.NoError(t, d.sql.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk))
	require.Equal(t, 1, fk)

	require.Equal(t, 1, d.sql.Stats().MaxOpenConnections)
}

// A fresh checkout has no runs/ yet; Open creates the directory.
func TestOpenCreatesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs", "nested", "verdict.db")

	d, err := Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, d.Close())
	require.FileExists(t, path)
}

func TestOpenRejectsQuestionMarkInPath(t *testing.T) {
	_, err := Open(t.Context(), filepath.Join(t.TempDir(), "a?b.db"))
	require.Error(t, err)
}

// A finished run is indexed from its state: the topic is the parent directory
// name, the paper count does not double count the enriched list.
func TestRecordRun(t *testing.T) {
	d := openTemp(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	st := &pipeline.State{
		Question: questionWork,
		Papers:   []model.Paper{{DOI: "10.1/a"}, {DOI: "10.1/b"}, {DOI: "10.1/c"}},
		Enriched: []model.EnrichedPaper{{DOI: "10.1/a"}, {DOI: "10.1/b"}},
		Report:   &model.Report{Verdict: verdictWeak, ConsensusStatus: "contested"},
	}

	require.NoError(t, d.RecordRun(ctx, filepath.Join("runs", topicCreatine, "does-it-work-1"), st, now))

	rows, err := d.Runs(ctx, topicCreatine)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, Row{
		Topic: topicCreatine, Run: "does-it-work-1", Dir: filepath.Join("runs", topicCreatine, "does-it-work-1"),
		Question: questionWork, Verdict: verdictWeak, Consensus: "contested", Papers: 2,
		GeneratedAt: "2026-11-01T12:00:00Z",
	}, rows[0])

	st.Report = nil
	require.Error(t, d.RecordRun(ctx, "runs/t/r", st, now), "a partial run has nothing to index")
}

func schemaVersion(t *testing.T, d *DB) int {
	t.Helper()

	var v int
	require.NoError(t, d.sql.QueryRowContext(t.Context(), `PRAGMA user_version`).Scan(&v))

	return v
}

// latestVersion is the highest embedded migration number.
func latestVersion(t *testing.T) int {
	t.Helper()

	ms, err := loadMigrations()
	require.NoError(t, err)
	require.NotEmpty(t, ms)

	return ms[len(ms)-1].version
}
