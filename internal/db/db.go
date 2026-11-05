// Package db is the run index (SQLite, pure-Go driver): topics, runs and
// verdicts — queries without walking runs/ on disk. The filesystem remains
// the source of truth (stage artifacts); the index is derived data that can
// be deleted and rebuilt at any time.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "modernc.org/sqlite"

	sq "github.com/Masterminds/squirrel"
)

// DB is the SQLite database of the run index.
type DB struct {
	sql *sql.DB
}

// Open opens (creating if needed) the database at path and migrates the
// schema to the latest version. Driver and pool settings are explicit: the
// defaults have no lock wait ("database is locked" on the first concurrent
// write), no foreign keys and a rollback journal that blocks readers.
func Open(ctx context.Context, path string) (*DB, error) {
	// the driver splits its DSN at the first '?'
	if strings.Contains(path, "?") {
		return nil, fmt.Errorf("database path %q must not contain '?'", path)
	}

	pragmas := url.Values{"_pragma": {"journal_mode(WAL)", "busy_timeout(5000)", "foreign_keys(1)"}}

	d, err := sql.Open("sqlite", path+"?"+pragmas.Encode())
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	// one connection: SQLite serializes writers anyway, and a second
	// connection would only turn contention into busy errors
	d.SetMaxOpenConns(1)

	if err := migrate(ctx, d); err != nil {
		_ = d.Close() // the migration error is the one worth returning

		return nil, err
	}

	return &DB{sql: d}, nil
}

func (db *DB) Close() error {
	if err := db.sql.Close(); err != nil {
		return fmt.Errorf("closing the database: %w", err)
	}

	return nil
}

// Row is one indexed run.
type Row struct {
	Topic       string `json:"topic"`
	Run         string `json:"run"`
	Dir         string `json:"dir"`
	Question    string `json:"question"`
	Verdict     string `json:"verdict"`
	Consensus   string `json:"consensus"`
	Papers      int    `json:"papers"`
	GeneratedAt string `json:"generated_at"`
}

// runsCols is the indexed column list; INSERT and SELECT share it, so the
// two stay in sync through one constant (spliced by concatenation, never
// fmt.Sprintf — % is the LIKE wildcard).
const runsCols = "topic, run, dir, question, verdict, consensus, papers, generated_at"

// recordSQL upserts a run row keyed by the run directory.
const recordSQL = `
	INSERT INTO runs (` + runsCols + `)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (dir) DO UPDATE SET
		topic = excluded.topic,
		run = excluded.run,
		question = excluded.question,
		verdict = excluded.verdict,
		consensus = excluded.consensus,
		papers = excluded.papers,
		generated_at = excluded.generated_at
`

// Record indexes a finished run (upsert keyed by the run directory).
func (db *DB) Record(ctx context.Context, r Row) error {
	_, err := db.sql.ExecContext(ctx, recordSQL,
		r.Topic, r.Run, r.Dir, r.Question, r.Verdict, r.Consensus, r.Papers, r.GeneratedAt)
	if err != nil {
		return fmt.Errorf("recording run %s: %w", r.Dir, err)
	}

	return nil
}

// Runs lists indexed runs, newest first; topic "" means all topics.
// Optional conditions are assembled by the squirrel builder (sq.Question
// placeholders for SQLite) — the standard rule for dynamic WHERE clauses.
func (db *DB) Runs(ctx context.Context, topic string) ([]Row, error) {
	b := sq.Select(runsCols).
		PlaceholderFormat(sq.Question).
		From("runs")

	if topic != "" {
		b = b.Where(sq.Eq{"topic": topic})
	}

	query, args, err := b.OrderBy("id DESC").ToSql()
	if err != nil {
		return nil, fmt.Errorf("building the runs query: %w", err)
	}

	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying runs: %w", err)
	}

	defer func() { _ = rows.Close() }() // read-only cursor: a close failure loses nothing

	var out []Row

	for rows.Next() {
		var r Row
		if err := rows.Scan(
			&r.Topic, &r.Run, &r.Dir, &r.Question,
			&r.Verdict, &r.Consensus, &r.Papers, &r.GeneratedAt,
		); err != nil {
			return nil, fmt.Errorf("reading a run row: %w", err)
		}

		out = append(out, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading runs: %w", err)
	}

	return out, nil
}
