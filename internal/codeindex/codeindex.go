// Package codeindex is the in-client, repo-agnostic code symbol index
// (sty_08b82e4a, epic:code-index). It lets the agent search symbols and fetch
// exact source slices instead of reading whole files — the same token win an
// external code-index MCP delivers, but as a first-party CLI capability the
// agent drives over Bash (no MCP server, no handshake, no lifecycle).
//
// The store is per-repo, lives at <repo>/.satellites/index.db, and is built by
// `satellites code index`. The driver is pure-Go (modernc.org/sqlite) so the
// release stays a CGO_ENABLED=0 static binary; symbol search rides SQLite FTS5.
//
// # Repo-agnostic by design — not Go-only
//
// THIS spike ships a single go/ast extractor (Extractor) because satellites is
// the convenient first test repo. That is a deliberate spike economy, NOT a
// narrowing of intent: the epic's non-negotiable is that the index works on ANY
// language. order:2 (sty_380a116d) swaps the go/ast extractor for CGo-free WASM
// tree-sitter on wazero — pure Go, preserving the static release — behind the
// same Extractor seam (Extract(path, src) -> []Symbol). The store, schema,
// search, and CLI surface here are language-neutral and carry forward unchanged;
// only the extractor is replaced.
package codeindex

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Symbol is one indexed declaration. Byte offsets give an exact source slice;
// line numbers are the human-facing file:line. Kind is the language-neutral
// declaration class (func, method, type, struct, interface, const, var) so the
// schema does not bake in Go-specific vocabulary.
type Symbol struct {
	Name      string
	Kind      string
	Signature string
	File      string // repo-relative, forward-slashed
	StartLine int
	EndLine   int
	StartByte int
	EndByte   int
}

// Store is the per-repo symbol index. The symbols table holds the rows; an FTS5
// external-content table mirrors it for search, rebuilt in one pass after a bulk
// load (index is build-once-per-run, so no per-row triggers are needed).
type Store struct {
	db *sql.DB
}

// Open opens (creating + migrating if absent) the index at path. Parent
// directories are created as needed, mirroring the engagement store so a fresh
// repo needs no setup step.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("codeindex: empty index path")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("codeindex: mkdir %s: %w", dir, err)
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("codeindex: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS symbols (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL,
    signature  TEXT NOT NULL DEFAULT '',
    file       TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line   INTEGER NOT NULL,
    start_byte INTEGER NOT NULL,
    end_byte   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file);
CREATE VIRTUAL TABLE IF NOT EXISTS symbols_fts USING fts5(
    name, signature, kind,
    content='symbols', content_rowid='id'
);
CREATE TABLE IF NOT EXISTS files (
    file       TEXT PRIMARY KEY,
    sha256     TEXT NOT NULL,
    indexed_at TEXT NOT NULL
);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("codeindex: migrate: %w", err)
	}
	return nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Replace wipes the index and writes syms as the new content, then rebuilds the
// FTS mirror — the whole-repo re-index `code index` performs. One transaction so
// a failed build never leaves a half-written index.
func (s *Store) Replace(syms []Symbol) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM symbols`); err != nil {
		return fmt.Errorf("codeindex: clear: %w", err)
	}
	// A whole-repo Replace invalidates the per-file content-hash cache: clear it
	// so a later incremental IndexRepo re-parses from a clean slate rather than
	// trusting stale hashes for rows it did not write.
	if _, err := tx.Exec(`DELETE FROM files`); err != nil {
		return fmt.Errorf("codeindex: clear files: %w", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO symbols
		(name, kind, signature, file, start_line, end_line, start_byte, end_byte)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, sym := range syms {
		if _, err := stmt.Exec(sym.Name, sym.Kind, sym.Signature, sym.File,
			sym.StartLine, sym.EndLine, sym.StartByte, sym.EndByte); err != nil {
			return fmt.Errorf("codeindex: insert %s: %w", sym.Name, err)
		}
	}
	// Rebuild the FTS index from the content table in one pass (external-content
	// FTS5 idiom — no per-row triggers, correct for a build-once index).
	if _, err := tx.Exec(`INSERT INTO symbols_fts(symbols_fts) VALUES('rebuild')`); err != nil {
		return fmt.Errorf("codeindex: fts rebuild: %w", err)
	}
	return tx.Commit()
}

// FileSymbols is one file's parse result for an incremental Reconcile: the
// repo-relative path, its content hash, and the symbols extracted from it.
type FileSymbols struct {
	File string
	Sha  string
	Syms []Symbol
}

// LoadFileHashes returns the stored content hash for every indexed file — the
// cache the incremental re-index consults to skip files whose content is
// unchanged since the last run.
func (s *Store) LoadFileHashes() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT file, sha256 FROM files`)
	if err != nil {
		return nil, fmt.Errorf("codeindex: load file hashes: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var file, sha string
		if err := rows.Scan(&file, &sha); err != nil {
			return nil, err
		}
		out[file] = sha
	}
	return out, rows.Err()
}

// Reconcile applies an incremental index update in one transaction: each changed
// file's symbols are replaced and its hash recorded; each deleted file's symbols
// and hash are removed; then the FTS mirror is rebuilt. A failed reconcile rolls
// back, never leaving a half-updated index. A no-op (nothing changed or deleted)
// returns without touching the database.
func (s *Store) Reconcile(changed []FileSymbols, deleted []string) error {
	if len(changed) == 0 && len(deleted) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	delSyms, err := tx.Prepare(`DELETE FROM symbols WHERE file = ?`)
	if err != nil {
		return err
	}
	defer delSyms.Close()
	insSym, err := tx.Prepare(`INSERT INTO symbols
		(name, kind, signature, file, start_line, end_line, start_byte, end_byte)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insSym.Close()
	upFile, err := tx.Prepare(`INSERT INTO files (file, sha256, indexed_at) VALUES (?, ?, ?)
		ON CONFLICT(file) DO UPDATE SET sha256 = excluded.sha256, indexed_at = excluded.indexed_at`)
	if err != nil {
		return err
	}
	defer upFile.Close()
	delFile, err := tx.Prepare(`DELETE FROM files WHERE file = ?`)
	if err != nil {
		return err
	}
	defer delFile.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, f := range changed {
		if _, err := delSyms.Exec(f.File); err != nil {
			return fmt.Errorf("codeindex: clear %s: %w", f.File, err)
		}
		for _, sym := range f.Syms {
			if _, err := insSym.Exec(sym.Name, sym.Kind, sym.Signature, sym.File,
				sym.StartLine, sym.EndLine, sym.StartByte, sym.EndByte); err != nil {
				return fmt.Errorf("codeindex: insert %s: %w", sym.Name, err)
			}
		}
		if _, err := upFile.Exec(f.File, f.Sha, now); err != nil {
			return fmt.Errorf("codeindex: record %s: %w", f.File, err)
		}
	}
	for _, d := range deleted {
		if _, err := delSyms.Exec(d); err != nil {
			return fmt.Errorf("codeindex: prune symbols %s: %w", d, err)
		}
		if _, err := delFile.Exec(d); err != nil {
			return fmt.Errorf("codeindex: prune file %s: %w", d, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO symbols_fts(symbols_fts) VALUES('rebuild')`); err != nil {
		return fmt.Errorf("codeindex: fts rebuild: %w", err)
	}
	return tx.Commit()
}

// Count returns the number of indexed symbols.
func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM symbols`).Scan(&n)
	return n, err
}

// Search returns symbols matching query, best-rank first. The primary path is an
// FTS5 prefix match on name/signature/kind (whole-identifier prefix, e.g.
// "resolveState" matches resolveStateDB); when that yields nothing it falls back
// to a substring match on name so a mid-identifier fragment still recalls. limit
// <= 0 means no limit.
func (s *Store) Search(query string, limit int) ([]Symbol, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("codeindex: empty query")
	}
	if match := ftsPrefixQuery(query); match != "" {
		syms, err := s.ftsSearch(match, limit)
		if err == nil && len(syms) > 0 {
			return syms, nil
		}
		// An FTS syntax error on exotic input is not fatal — fall through to LIKE.
	}
	return s.likeSearch(query, limit)
}

func (s *Store) ftsSearch(match string, limit int) ([]Symbol, error) {
	q := `SELECT s.name, s.kind, s.signature, s.file, s.start_line, s.end_line, s.start_byte, s.end_byte
		FROM symbols_fts f JOIN symbols s ON s.id = f.rowid
		WHERE symbols_fts MATCH ? ORDER BY rank`
	args := []any{match}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return s.querySymbols(q, args...)
}

func (s *Store) likeSearch(query string, limit int) ([]Symbol, error) {
	q := `SELECT name, kind, signature, file, start_line, end_line, start_byte, end_byte
		FROM symbols WHERE name LIKE ? ORDER BY name`
	args := []any{"%" + query + "%"}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return s.querySymbols(q, args...)
}

// ByName returns the symbols whose name matches exactly — the lookup behind
// `code symbol <name>` (more than one row when a name is overloaded, e.g. a
// method name shared across receivers).
func (s *Store) ByName(name string) ([]Symbol, error) {
	return s.querySymbols(
		`SELECT name, kind, signature, file, start_line, end_line, start_byte, end_byte
		 FROM symbols WHERE name = ? ORDER BY file, start_line`, strings.TrimSpace(name))
}

func (s *Store) querySymbols(q string, args ...any) ([]Symbol, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Symbol
	for rows.Next() {
		var sym Symbol
		if err := rows.Scan(&sym.Name, &sym.Kind, &sym.Signature, &sym.File,
			&sym.StartLine, &sym.EndLine, &sym.StartByte, &sym.EndByte); err != nil {
			return nil, err
		}
		out = append(out, sym)
	}
	return out, rows.Err()
}

// ftsPrefixQuery turns a user query into an FTS5 prefix MATCH: each
// alphanumeric run becomes a quoted prefix term (term*), joined by AND. A query
// with no usable token returns "" so the caller skips FTS and goes to LIKE.
func ftsPrefixQuery(query string) string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
	})
	var terms []string
	for _, f := range fields {
		if f != "" {
			terms = append(terms, `"`+f+`"*`)
		}
	}
	return strings.Join(terms, " ")
}
