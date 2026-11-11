package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrationsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := s.CreateProduct(ctx, NewProduct{Shop: "otto", URL: "u", Name: "n"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s.Close()

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("schema_migrations rows = %d, want 1", n)
	}
	ps, err := s.ListProducts(ctx)
	if err != nil || len(ps) != 1 {
		t.Fatalf("data lost after reopen: %v len=%d", err, len(ps))
	}
}

func TestForeignKeysEnabled(t *testing.T) {
	ctx := context.Background()
	for name, path := range map[string]string{
		"memory": ":memory:",
		"file":   filepath.Join(t.TempDir(), "fk.db"),
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var fk int
			if err := s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
				t.Fatal(err)
			}
			if fk != 1 {
				t.Errorf("foreign_keys = %d, want 1", fk)
			}
		})
	}
}

func TestMigrateBadSQLRollsBack(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = applyMigrations(ctx, s.db, []migration{{version: 99, name: "bad", sql: "CREATE TABLE x(a); BOGUS;"}}, s.now)
	if err == nil {
		t.Fatal("expected error")
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name='x'`).Scan(&n)
	if n != 0 {
		t.Error("failed migration was not rolled back")
	}
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=99`).Scan(&n)
	if n != 0 {
		t.Error("failed migration recorded")
	}
}

func TestLoadMigrations(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].version != 1 {
		t.Fatalf("unexpected migrations: %+v", ms)
	}
}
