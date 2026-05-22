package document

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store wraps DB-backed document operations.
type Store struct {
	DB *sql.DB
}

// New returns a Store bound to the given database/sql handle.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// UpsertInput is the write payload for Upsert. ViaInternalSeed=true is
// the only path that may write at scope=system; everything else gets
// ErrScopeReadonly. Verbs always pass false.
type UpsertInput struct {
	Key             Key
	Body            string
	CreatedBy       string
	ViaInternalSeed bool
}

// GetOptions controls what Get returns. Version semantics:
//
//	Version == 0       → latest active version (default)
//	Version >= 1       → that exact version row
//	AllVersions == true → ignore Version; return the full history
//
// IncludeDeleted=true surfaces a tombstoned latest. Used by AllVersions
// + delete-aware paths; the default verb call leaves it false.
type GetOptions struct {
	Version        int
	AllVersions    bool
	IncludeDeleted bool
}

// GetResult bundles the document row with the requested version slice.
// When AllVersions=false, Versions contains zero or one row.
type GetResult struct {
	Document Document
	Versions []Version
}

// Upsert creates a new version row and advances documents.latest_version.
// On first call for a Key, a documents row is inserted; subsequent calls
// just append to document_versions. Versions are strictly monotonic per
// document_id; concurrency is serialised via SELECT … FOR UPDATE on the
// documents row.
func (s *Store) Upsert(ctx context.Context, in UpsertInput, now time.Time) (Document, Version, error) {
	if err := in.Key.Validate(); err != nil {
		return Document{}, Version{}, err
	}
	if in.Key.Scope == ScopeSystem && !in.ViaInternalSeed {
		return Document{}, Version{}, ErrScopeReadonly
	}
	now = now.UTC()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, Version{}, fmt.Errorf("document: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit path handles success

	doc, err := lockOrInsertDocument(ctx, tx, in.Key, now)
	if err != nil {
		return Document{}, Version{}, err
	}

	nextVersion := doc.LatestVersion + 1
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO document_versions (document_id, version, body, status, created_at, created_by)
        VALUES ($1, $2, $3, 'active', $4, $5)
    `, doc.ID, nextVersion, in.Body, now, in.CreatedBy); err != nil {
		return Document{}, Version{}, fmt.Errorf("document: insert version: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
        UPDATE documents SET latest_version = $1, updated_at = $2 WHERE id = $3
    `, nextVersion, now, doc.ID); err != nil {
		return Document{}, Version{}, fmt.Errorf("document: update latest: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Document{}, Version{}, fmt.Errorf("document: commit: %w", err)
	}

	doc.LatestVersion = nextVersion
	doc.UpdatedAt = now
	v := Version{
		DocumentID: doc.ID,
		Version:    nextVersion,
		Body:       in.Body,
		Status:     StatusActive,
		CreatedAt:  now,
		CreatedBy:  in.CreatedBy,
	}
	return doc, v, nil
}

// Get returns the document plus the requested version slice. With
// default GetOptions, it returns the single latest-active version; the
// caller sees ErrNotFound when the document doesn't exist or when only
// a soft-deleted latest is present and IncludeDeleted is false.
func (s *Store) Get(ctx context.Context, key Key, opts GetOptions) (GetResult, error) {
	if err := key.Validate(); err != nil {
		return GetResult{}, err
	}
	doc, err := s.lookupDocument(ctx, key)
	if err != nil {
		return GetResult{}, err
	}

	if opts.AllVersions {
		vs, err := s.listVersions(ctx, doc.ID)
		if err != nil {
			return GetResult{}, err
		}
		return GetResult{Document: doc, Versions: vs}, nil
	}

	v, err := s.getVersion(ctx, doc.ID, opts.Version, opts.IncludeDeleted)
	if err != nil {
		return GetResult{}, err
	}
	return GetResult{Document: doc, Versions: []Version{v}}, nil
}

// Delete soft-deletes a document by appending a tombstone version
// (status='deleted'). History remains retrievable via Get with
// AllVersions=true. System scope is rejected unless via seed.
func (s *Store) Delete(ctx context.Context, key Key, deletedBy string, viaInternalSeed bool, now time.Time) (Document, Version, error) {
	if err := key.Validate(); err != nil {
		return Document{}, Version{}, err
	}
	if key.Scope == ScopeSystem && !viaInternalSeed {
		return Document{}, Version{}, ErrScopeReadonly
	}
	now = now.UTC()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, Version{}, fmt.Errorf("document: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	doc, err := lockExistingDocument(ctx, tx, key)
	if err != nil {
		return Document{}, Version{}, err
	}

	nextVersion := doc.LatestVersion + 1
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO document_versions (document_id, version, body, status, created_at, created_by)
        VALUES ($1, $2, '', 'deleted', $3, $4)
    `, doc.ID, nextVersion, now, deletedBy); err != nil {
		return Document{}, Version{}, fmt.Errorf("document: insert tombstone: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
        UPDATE documents SET latest_version = $1, updated_at = $2 WHERE id = $3
    `, nextVersion, now, doc.ID); err != nil {
		return Document{}, Version{}, fmt.Errorf("document: update latest: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Document{}, Version{}, fmt.Errorf("document: commit: %w", err)
	}

	doc.LatestVersion = nextVersion
	doc.UpdatedAt = now
	v := Version{
		DocumentID: doc.ID,
		Version:    nextVersion,
		Status:     StatusDeleted,
		CreatedAt:  now,
		CreatedBy:  deletedBy,
	}
	return doc, v, nil
}

// lockOrInsertDocument resolves the documents row for an upsert: locks
// the existing row with SELECT … FOR UPDATE so concurrent upserts
// serialise, or inserts a fresh row when the document is new. The
// caller holds the transaction.
//
// Race-free creation uses a serialise-on-(scope, name) advisory lock:
// concurrent goroutines hitting a non-existent key would otherwise all
// pass the FOR UPDATE probe (nothing to lock yet) and race on the
// INSERT, only one of which would survive the partial-unique index.
// Holding pg_advisory_xact_lock keyed on (scope, name) for the
// existence-check + insert window serialises them within the txn.
func lockOrInsertDocument(ctx context.Context, tx *sql.Tx, key Key, now time.Time) (Document, error) {
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		string(key.Scope), key.WorkspaceID+"\x1f"+key.ProjectID+"\x1f"+key.Name,
	); err != nil {
		return Document{}, fmt.Errorf("document: advisory lock: %w", err)
	}
	doc, err := lockDocumentByKey(ctx, tx, key)
	if err == nil {
		return doc, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Document{}, err
	}
	id := NewID()
	wsArg := nullStr(key.WorkspaceID)
	pjArg := nullStr(key.ProjectID)
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO documents
            (id, scope, workspace_id, project_id, name, latest_version, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, 0, $6, $6)
    `, id, string(key.Scope), wsArg, pjArg, key.Name, now); err != nil {
		return Document{}, fmt.Errorf("document: insert: %w", err)
	}
	return lockDocumentByKey(ctx, tx, key)
}

// lockExistingDocument is the delete-path variant: errors out when the
// document doesn't exist, instead of inserting a fresh row.
func lockExistingDocument(ctx context.Context, tx *sql.Tx, key Key) (Document, error) {
	return lockDocumentByKey(ctx, tx, key)
}

func lockDocumentByKey(ctx context.Context, tx *sql.Tx, key Key) (Document, error) {
	wsArg := nullStr(key.WorkspaceID)
	pjArg := nullStr(key.ProjectID)
	row := tx.QueryRowContext(ctx, `
        SELECT id, scope, COALESCE(workspace_id,''), COALESCE(project_id,''),
               name, latest_version, created_at, updated_at
        FROM documents
        WHERE scope = $1
          AND workspace_id IS NOT DISTINCT FROM $2
          AND project_id   IS NOT DISTINCT FROM $3
          AND name = $4
        FOR UPDATE
    `, string(key.Scope), wsArg, pjArg, key.Name)
	return scanDocumentRow(row)
}

func (s *Store) lookupDocument(ctx context.Context, key Key) (Document, error) {
	wsArg := nullStr(key.WorkspaceID)
	pjArg := nullStr(key.ProjectID)
	row := s.DB.QueryRowContext(ctx, `
        SELECT id, scope, COALESCE(workspace_id,''), COALESCE(project_id,''),
               name, latest_version, created_at, updated_at
        FROM documents
        WHERE scope = $1
          AND workspace_id IS NOT DISTINCT FROM $2
          AND project_id   IS NOT DISTINCT FROM $3
          AND name = $4
    `, string(key.Scope), wsArg, pjArg, key.Name)
	return scanDocumentRow(row)
}

func (s *Store) getVersion(ctx context.Context, docID string, version int, includeDeleted bool) (Version, error) {
	if version == 0 {
		// "Default latest" semantics: peek at the highest version row.
		// If it's a tombstone and the caller didn't ask for deleted, the
		// document is currently deleted — return ErrNotFound. We don't
		// fall back to the most recent pre-tombstone active version; a
		// deleted document is gone from the user-facing surface.
		row := s.DB.QueryRowContext(ctx, `
            SELECT document_id, version, body, status, created_at, created_by
            FROM document_versions
            WHERE document_id = $1
            ORDER BY version DESC LIMIT 1
        `, docID)
		v, err := scanVersionRow(row)
		if err != nil {
			return Version{}, err
		}
		if v.Status == StatusDeleted && !includeDeleted {
			return Version{}, ErrNotFound
		}
		return v, nil
	}
	row := s.DB.QueryRowContext(ctx, `
        SELECT document_id, version, body, status, created_at, created_by
        FROM document_versions
        WHERE document_id = $1 AND version = $2
    `, docID, version)
	return scanVersionRow(row)
}

func (s *Store) listVersions(ctx context.Context, docID string) ([]Version, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT document_id, version, body, status, created_at, created_by
        FROM document_versions
        WHERE document_id = $1
        ORDER BY version ASC
    `, docID)
	if err != nil {
		return nil, fmt.Errorf("document: list versions: %w", err)
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v, err := scanVersionRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDocumentRow(rs rowScanner) (Document, error) {
	var (
		d       Document
		scopeS  string
		statusS string // unused here; kept symmetric with version scan if extended
	)
	_ = statusS
	if err := rs.Scan(&d.ID, &scopeS, &d.WorkspaceID, &d.ProjectID,
		&d.Name, &d.LatestVersion, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Document{}, ErrNotFound
		}
		return Document{}, fmt.Errorf("document: scan: %w", err)
	}
	d.Scope = Scope(scopeS)
	return d, nil
}

func scanVersionRow(rs *sql.Row) (Version, error) {
	v, err := scanVersionCommon(rs)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	return v, err
}

func scanVersionRows(rs *sql.Rows) (Version, error) {
	return scanVersionCommon(rs)
}

func scanVersionCommon(rs rowScanner) (Version, error) {
	var (
		v       Version
		statusS string
	)
	if err := rs.Scan(&v.DocumentID, &v.Version, &v.Body, &statusS, &v.CreatedAt, &v.CreatedBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Version{}, err
		}
		return Version{}, fmt.Errorf("document: scan version: %w", err)
	}
	v.Status = Status(statusS)
	return v, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
