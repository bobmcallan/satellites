// Package db wires the satellites V5 schema and runs migrations against Postgres.
//
// Migration files live under internal/db/migrations/ and are embedded into the
// binary via go:embed, so deploying satellites-server is a single-binary drop —
// no separate migration assets to ship.
package db

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // postgres driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrateUp runs all pending migrations against the database at dsn.
// Returns nil when already at the latest version.
func MigrateUp(dsn string) error {
	return runMigrate(dsn, func(m *migrate.Migrate) error { return m.Up() })
}

// MigrateDown rolls every migration back. Used by tests and local dev resets.
func MigrateDown(dsn string) error {
	return runMigrate(dsn, func(m *migrate.Migrate) error { return m.Down() })
}

func runMigrate(dsn string, op func(*migrate.Migrate) error) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("db: open embedded migrations: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("db: open migrator: %w", err)
	}
	defer m.Close()

	if err := op(m); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: migrate: %w", err)
	}
	return nil
}
