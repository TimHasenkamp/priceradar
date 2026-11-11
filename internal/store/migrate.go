package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded NNNN_name.sql files sorted by version.
func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		base := strings.TrimSuffix(e.Name(), ".sql")
		num, name, _ := strings.Cut(base, "_")
		v, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("store: bad migration file name %q", e.Name())
		}
		b, err := migrationFS.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("store: read migration %q: %w", e.Name(), err)
		}
		out = append(out, migration{version: v, name: name, sql: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// applyMigrations runs every not-yet-applied migration, each in its own transaction.
func applyMigrations(ctx context.Context, db *sql.DB, ms []migration, now func() time.Time) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at INTEGER)`); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}
	for _, m := range ms {
		var exists int
		err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("store: check migration %d: %w", m.version, err)
		}
		if exists > 0 {
			continue
		}
		if err := applyOne(ctx, db, m, now()); err != nil {
			return fmt.Errorf("store: migration %04d_%s: %w", m.version, m.name, err)
		}
	}
	return nil
}

func applyOne(ctx context.Context, db *sql.DB, m migration, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, now.UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}
