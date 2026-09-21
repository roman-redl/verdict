// Package db is the run index (SQLite, pure-Go driver): topics, runs and
// verdicts — queries without walking runs/ on disk. The filesystem remains
// the source of truth (stage artifacts); the index is derived data that can
// be deleted and rebuilt at any time.
package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
}

func Open(path string) (*DB, error) {
	d, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS runs (
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
	)`); err != nil {
		d.Close()
		return nil, err
	}
	return &DB{sql: d}, nil
}

func (db *DB) Close() error { return db.sql.Close() }

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

// Record indexes a finished run (upsert keyed by the run directory).
func (db *DB) Record(r Row) error {
	_, err := db.sql.Exec(`INSERT INTO runs (topic, run, dir, question, verdict, consensus, papers, generated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dir) DO UPDATE SET
			topic=excluded.topic, run=excluded.run, question=excluded.question,
			verdict=excluded.verdict, consensus=excluded.consensus,
			papers=excluded.papers, generated_at=excluded.generated_at`,
		r.Topic, r.Run, r.Dir, r.Question, r.Verdict, r.Consensus, r.Papers, r.GeneratedAt)
	return err
}

// Runs lists indexed runs, newest first; topic "" means all topics.
func (db *DB) Runs(topic string) ([]Row, error) {
	q := `SELECT topic, run, dir, question, verdict, consensus, papers, generated_at FROM runs`
	args := []any{}
	if topic != "" {
		q += ` WHERE topic = ?`
		args = append(args, topic)
	}
	q += ` ORDER BY id DESC`
	rows, err := db.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.Topic, &r.Run, &r.Dir, &r.Question, &r.Verdict, &r.Consensus, &r.Papers, &r.GeneratedAt); err != nil {
			return nil, fmt.Errorf("db: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// stamp formats the index timestamp column.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
