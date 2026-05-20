package db

import (
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// TestMigrationsEmbedParse confirms the embedded migration files are
// well-formed (parseable by golang-migrate) without requiring a live
// Postgres. The integration test framework (sty_e8cfd635) covers the
// against-a-real-DB path.
func TestMigrationsEmbedParse(t *testing.T) {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("open embedded migrations: %v", err)
	}
	defer src.Close()

	v, err := src.First()
	if err != nil {
		t.Fatalf("first migration version: %v", err)
	}
	if v == 0 {
		t.Fatalf("expected first version > 0, got 0")
	}

	if r, _, err := src.ReadUp(v); err != nil {
		t.Fatalf("read up %d: %v", v, err)
	} else {
		r.Close()
	}

	if r, _, err := src.ReadDown(v); err != nil {
		t.Fatalf("read down %d: %v", v, err)
	} else {
		r.Close()
	}
}
