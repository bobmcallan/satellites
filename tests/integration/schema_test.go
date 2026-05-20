//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestSchemaApplied — one Env, multiple sections; each section calls
// Reset(t, env) for clean data injection. Validates migration apply,
// schema shape, append-only enforcement, and verb dispatch reachability
// against a live Postgres.
func TestSchemaApplied(t *testing.T) {
	env := testbootstrap.SetUp(t)

	t.Run("four tables exist after migrate-up", func(t *testing.T) {
		testbootstrap.Reset(t, env)

		rows, err := env.DB.Query(`
            SELECT tablename FROM pg_tables
             WHERE schemaname = 'public'
               AND tablename IN ('stories', 'tools', 'reviews', 'evidence')
             ORDER BY tablename
        `)
		if err != nil {
			t.Fatalf("query pg_tables: %v", err)
		}
		defer rows.Close()

		var seen []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			seen = append(seen, name)
		}

		want := []string{"evidence", "reviews", "stories", "tools"}
		if len(seen) != 4 {
			t.Fatalf("expected 4 tables, got %v", seen)
		}
		for i, w := range want {
			if seen[i] != w {
				t.Fatalf("table %d: got %s want %s", i, seen[i], w)
			}
		}
	})

	t.Run("evidence is append-only", func(t *testing.T) {
		testbootstrap.Reset(t, env)

		if _, err := env.DB.Exec(`
            INSERT INTO evidence (id, story_id, kind, body)
            VALUES ('evi_test001', 'sty_test001', 'note', 'hello')
        `); err != nil {
			t.Fatalf("insert: %v", err)
		}

		if _, err := env.DB.Exec(`UPDATE evidence SET body = 'changed' WHERE id = 'evi_test001'`); err == nil {
			t.Fatal("expected UPDATE to fail on evidence (append-only)")
		}

		if _, err := env.DB.Exec(`DELETE FROM evidence WHERE id = 'evi_test001'`); err == nil {
			t.Fatal("expected DELETE to fail on evidence (append-only)")
		}
	})

	t.Run("verb dispatch reachable", func(t *testing.T) {
		testbootstrap.Reset(t, env)

		resp, err := verb.Dispatch(context.Background(), "version", nil)
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var info verb.VersionInfo
		if err := json.Unmarshal(resp, &info); err != nil {
			t.Fatal(err)
		}
		if info.Version == "" {
			t.Fatal("empty version")
		}
	})
}

// TestIsolation — proves clean injection between sections. Section A
// inserts a row; section B asserts the table is empty after Reset.
// Either order would pass: data does not leak across sections.
func TestIsolation(t *testing.T) {
	env := testbootstrap.SetUp(t)

	t.Run("section A inserts a story", func(t *testing.T) {
		testbootstrap.Reset(t, env)

		if _, err := env.DB.Exec(`
            INSERT INTO stories (id, project_id, title)
            VALUES ('sty_iso_a', 'proj_iso', 'section A story')
        `); err != nil {
			t.Fatal(err)
		}

		var count int
		if err := env.DB.QueryRow(`SELECT count(*) FROM stories`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("section A: expected 1 story, got %d", count)
		}
	})

	t.Run("section B sees clean state after reset", func(t *testing.T) {
		testbootstrap.Reset(t, env)

		var count int
		if err := env.DB.QueryRow(`SELECT count(*) FROM stories`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("section B: expected 0 stories after reset, got %d", count)
		}
	})
}
