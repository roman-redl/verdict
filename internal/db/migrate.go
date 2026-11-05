package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// Migrations are numbered SQL files (0001_name.sql, 0002_name.sql, ...);
// the applied version is kept in PRAGMA user_version.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded files ordered by version.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("listing migrations: %w", err)
	}

	out := make([]migration, 0, len(entries))

	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")

		version, err := strconv.Atoi(num)
		if !ok || err != nil {
			return nil, fmt.Errorf("migration %q is not named <number>_<name>.sql", e.Name())
		}

		body, err := fs.ReadFile(migrationFiles, "migrations/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("reading migration %s: %w", e.Name(), err)
		}

		out = append(out, migration{version: version, name: e.Name(), sql: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })

	return out, nil
}

// migrate brings the schema to the latest version. A database created before
// migrations existed (it has the runs table but user_version 0) already holds
// the first migration's schema and is adopted at version 1 in place.
func migrate(ctx context.Context, d *sql.DB) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	var current int
	if err := d.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("reading the schema version: %w", err)
	}

	if current == 0 {
		legacy, err := hasTable(ctx, d, "runs")
		if err != nil {
			return err
		}

		if legacy {
			current = 1
			if err := setVersion(ctx, d, current); err != nil {
				return err
			}
		}
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}

		if err := apply(ctx, d, m); err != nil {
			return err
		}
	}

	return nil
}

func hasTable(ctx context.Context, d *sql.DB, name string) (bool, error) {
	var n int

	err := d.QueryRowContext(ctx, `
		SELECT count(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`, name).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("looking for table %s: %w", name, err)
	}

	return n > 0, nil
}

// execer is what both *sql.DB and *sql.Tx offer.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func setVersion(ctx context.Context, x execer, version int) error {
	// PRAGMA takes no bound parameters; the version is an integer we control.
	if _, err := x.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(version)); err != nil {
		return fmt.Errorf("storing schema version %d: %w", version, err)
	}

	return nil
}

// apply runs one migration and records its version in one transaction.
func apply(ctx context.Context, d *sql.DB, m migration) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting migration %s: %w", m.name, err)
	}

	defer func() { _ = tx.Rollback() }() // a no-op after Commit; the error that matters is already returned

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("applying migration %s: %w", m.name, err)
	}

	if err := setVersion(ctx, tx, m.version); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration %s: %w", m.name, err)
	}

	return nil
}
