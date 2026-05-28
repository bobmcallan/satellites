package document

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
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
//
// Type names the documents.type discriminator the row should carry on
// first insert. Empty defaults to TypeDocument. Documents and skills
// share the (scope, name) namespace, so an upsert against an existing
// row whose stored type disagrees with this value returns
// ErrTypeMismatch.
type UpsertInput struct {
	Key             Key
	Type            string
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
// On first call for a Key, a documents row is inserted (with type='document');
// subsequent calls just append to document_versions. Versions are
// strictly monotonic per document_id; concurrency is serialised via
// SELECT … FOR UPDATE on the documents row.
//
// This is the document_upsert verb's path — for stories see CreateStory
// and UpdateStory below, which key by id rather than by name.
func (s *Store) Upsert(ctx context.Context, in UpsertInput, now time.Time) (Document, Version, error) {
	if err := in.Key.Validate(); err != nil {
		return Document{}, Version{}, err
	}
	if in.Key.Scope == ScopeSystem && !in.ViaInternalSeed {
		return Document{}, Version{}, ErrScopeReadonly
	}
	docType := in.Type
	if docType == "" {
		docType = TypeDocument
	}
	if docType != TypeDocument && docType != TypeSkill {
		return Document{}, Version{}, fmt.Errorf("document: upsert: unsupported type %q (allowed: document, skill)", docType)
	}
	now = now.UTC()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, Version{}, fmt.Errorf("document: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit path handles success

	doc, err := lockOrInsertDocument(ctx, tx, in.Key, docType, now)
	if err != nil {
		return Document{}, Version{}, err
	}
	if doc.Type != docType {
		return Document{}, Version{}, fmt.Errorf("%w: existing row at (scope=%s, name=%s) is type=%s, upsert requested type=%s",
			ErrTypeMismatch, in.Key.Scope, in.Key.Name, doc.Type, docType)
	}

	// Body-equality short-circuit. When the latest version is active and
	// its body matches the incoming bytes, no new version is written —
	// the substrate stays byte-exact on re-pushes from .satellites/
	// documents/, satellites_seed reconciliation, and any other caller
	// that repeats an unchanged write. Tags are handled separately by
	// SetDocumentTags, which has its own equality check.
	if doc.LatestVersion > 0 {
		existing, lookupErr := latestVersionInTx(ctx, tx, doc.ID)
		if lookupErr == nil && existing.Status == StatusActive && existing.Body == in.Body {
			if err := tx.Commit(); err != nil {
				return Document{}, Version{}, fmt.Errorf("document: commit: %w", err)
			}
			return doc, existing, nil
		}
		if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
			return Document{}, Version{}, lookupErr
		}
	}

	doc, v, err := appendVersion(ctx, tx, doc, in.Body, in.CreatedBy, now, StatusActive)
	if err != nil {
		return Document{}, Version{}, err
	}

	if err := tx.Commit(); err != nil {
		return Document{}, Version{}, fmt.Errorf("document: commit: %w", err)
	}
	return doc, v, nil
}

// latestVersionInTx reads the most recent document_versions row for an
// id inside the supplied transaction. Used by Upsert's idempotency
// short-circuit, which must see the same snapshot the subsequent
// appendVersion call would lock.
func latestVersionInTx(ctx context.Context, tx *sql.Tx, id string) (Version, error) {
	row := tx.QueryRowContext(ctx, `
        SELECT document_id, version, body, status, created_at, created_by
        FROM document_versions
        WHERE document_id = $1
        ORDER BY version DESC LIMIT 1
    `, id)
	return scanVersionRow(row)
}

// CreateStoryInput is the write payload for CreateStory. All metadata
// fields default to sensible substrate values when empty: status →
// 'backlog', priority → 'medium', category → 'feature', tags → [].
type CreateStoryInput struct {
	ProjectID          string
	WorkspaceID        string
	ParentID           string
	Title              string
	Body               string
	AcceptanceCriteria string
	Status             string
	Priority           string
	Category           string
	Tags               []string
	CreatedBy          string
}

// CreateStory inserts a new type='story' documents row plus its v1
// document_versions body. Story ids are minted in the sty_<8hex> form
// so existing evidence.story_id rows stay matchable.
func (s *Store) CreateStory(ctx context.Context, in CreateStoryInput, now time.Time) (Document, error) {
	if in.ProjectID == "" {
		return Document{}, fmt.Errorf("document: project_id required")
	}
	if in.WorkspaceID == "" {
		return Document{}, fmt.Errorf("document: workspace_id required")
	}
	if in.Title == "" {
		return Document{}, fmt.Errorf("document: title required")
	}
	if in.Status == "" {
		in.Status = "backlog"
	}
	if in.Priority == "" {
		in.Priority = "medium"
	}
	if in.Category == "" {
		in.Category = "feature"
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	now = now.UTC()
	id := newStoryID()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, fmt.Errorf("document: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `
        INSERT INTO documents
            (id, scope, workspace_id, project_id, name, latest_version,
             type, tags, status, priority, category, parent_id, acceptance_criteria,
             created_at, updated_at)
        VALUES ($1, 'project', $2, $3, $4, 1,
                'story', $5, $6, $7, $8, $9, $10,
                $11, $11)
    `, id, in.WorkspaceID, in.ProjectID, in.Title,
		pq.Array(in.Tags), in.Status, in.Priority, in.Category, nullStr(in.ParentID), in.AcceptanceCriteria,
		now); err != nil {
		return Document{}, fmt.Errorf("document: insert story: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO document_versions (document_id, version, body, status, created_at, created_by)
        VALUES ($1, 1, $2, 'active', $3, $4)
    `, id, in.Body, now, in.CreatedBy); err != nil {
		return Document{}, fmt.Errorf("document: insert story v1: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Document{}, fmt.Errorf("document: commit story: %w", err)
	}

	return Document{
		ID: id, Type: TypeStory, Scope: ScopeProject,
		WorkspaceID: in.WorkspaceID, ProjectID: in.ProjectID,
		Name: in.Title, LatestVersion: 1,
		Tags: in.Tags, Status: in.Status,
		Priority: in.Priority, Category: in.Category,
		ParentID: in.ParentID, AcceptanceCriteria: in.AcceptanceCriteria,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// UpdateStoryPatch carries the mutable fields for UpdateStory. Each
// pointer field is "set when non-nil"; nil means "leave existing
// value alone". A non-nil Body advances latest_version and appends a
// new document_versions row.
type UpdateStoryPatch struct {
	ParentID           *string
	Title              *string // → documents.name
	Body               *string // → new document_versions row
	AcceptanceCriteria *string
	Status             *string
	Priority           *string
	Category           *string
	Tags               *[]string
	CreatedBy          string // stamped on the new version row when Body is non-nil
}

// UpdateStory patches the documents row at id and (when patch.Body is
// non-nil) appends a new version. Returns the updated row.
func (s *Store) UpdateStory(ctx context.Context, id string, patch UpdateStoryPatch, now time.Time) (Document, error) {
	now = now.UTC()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, fmt.Errorf("document: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	doc, err := lockDocumentByID(ctx, tx, id)
	if err != nil {
		return Document{}, err
	}
	if doc.Type != TypeStory {
		return Document{}, fmt.Errorf("document: %s is not a story", id)
	}

	if patch.Title != nil {
		if *patch.Title == "" {
			return Document{}, fmt.Errorf("document: title cannot be cleared")
		}
		doc.Name = *patch.Title
	}
	if patch.ParentID != nil {
		doc.ParentID = *patch.ParentID
	}
	if patch.AcceptanceCriteria != nil {
		doc.AcceptanceCriteria = *patch.AcceptanceCriteria
	}
	if patch.Status != nil {
		doc.Status = *patch.Status
	}
	if patch.Priority != nil {
		doc.Priority = *patch.Priority
	}
	if patch.Category != nil {
		doc.Category = *patch.Category
	}
	if patch.Tags != nil {
		doc.Tags = *patch.Tags
		if doc.Tags == nil {
			doc.Tags = []string{}
		}
	}

	if patch.Body != nil {
		next := doc.LatestVersion + 1
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO document_versions (document_id, version, body, status, created_at, created_by)
            VALUES ($1, $2, $3, 'active', $4, $5)
        `, id, next, *patch.Body, now, patch.CreatedBy); err != nil {
			return Document{}, fmt.Errorf("document: append version: %w", err)
		}
		doc.LatestVersion = next
	}

	if _, err := tx.ExecContext(ctx, `
        UPDATE documents SET
            name = $1, parent_id = $2, acceptance_criteria = $3,
            status = $4, priority = $5, category = $6,
            tags = $7, latest_version = $8, updated_at = $9
        WHERE id = $10
    `, doc.Name, nullStr(doc.ParentID), doc.AcceptanceCriteria,
		doc.Status, nullStr(doc.Priority), nullStr(doc.Category),
		pq.Array(doc.Tags), doc.LatestVersion, now, id); err != nil {
		return Document{}, fmt.Errorf("document: update story: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Document{}, fmt.Errorf("document: commit: %w", err)
	}
	doc.UpdatedAt = now
	return doc, nil
}

// LatestVersionStatus returns the status of the most recent version
// row for the given document id, regardless of whether it is active or
// deleted. Useful for callers that need to distinguish a live document
// from one whose latest version is a soft-delete tombstone — the
// `*WithLatestBody` helpers filter to active versions and so cannot
// surface a tombstone on their own. Returns ErrNotFound when the
// document has no versions at all.
func (s *Store) LatestVersionStatus(ctx context.Context, id string) (Status, error) {
	var status Status
	row := s.DB.QueryRowContext(ctx, `
        SELECT status FROM document_versions
        WHERE document_id = $1
        ORDER BY version DESC LIMIT 1
    `, id)
	if err := row.Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("document: latest version status: %w", err)
	}
	return status, nil
}

// SetDocumentTags replaces the tags array on a type='document' row.
// Stories carry tags through UpdateStory; this is the equivalent path
// for free-form documents (notably the principle-tag flow). Reject on
// non-document rows so the story update path stays the only writer of
// tags on stories.
func (s *Store) SetDocumentTags(ctx context.Context, id string, tags []string, now time.Time) (Document, error) {
	now = now.UTC()
	if tags == nil {
		tags = []string{}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, fmt.Errorf("document: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit path handles success

	doc, err := lockDocumentByID(ctx, tx, id)
	if err != nil {
		return Document{}, err
	}
	if doc.Type != TypeDocument {
		return Document{}, fmt.Errorf("document: SetDocumentTags: id=%s is type=%s, expected document", id, doc.Type)
	}
	// Tag-equality short-circuit. Re-applying the same tag set is a
	// no-op (no UPDATE, no updated_at bump) — keeps `satellites
	// documents upload` idempotent end-to-end on the documents row, not
	// just on document_versions.
	if stringSlicesEqualUnordered(doc.Tags, tags) {
		if err := tx.Commit(); err != nil {
			return Document{}, fmt.Errorf("document: commit: %w", err)
		}
		return doc, nil
	}
	if _, err := tx.ExecContext(ctx, `
        UPDATE documents SET tags = $1, updated_at = $2 WHERE id = $3
    `, pq.Array(tags), now, id); err != nil {
		return Document{}, fmt.Errorf("document: set tags: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Document{}, fmt.Errorf("document: commit: %w", err)
	}
	doc.Tags = append([]string(nil), tags...)
	doc.UpdatedAt = now
	return doc, nil
}

// stringSlicesEqualUnordered compares two string slices ignoring order
// and duplicates — Postgres array equality semantics treat
// `{a,b} = {b,a}` as true on `=`, but ARRAY[...] literals compare by
// position, so the in-Go check stays unordered to match the column's
// set-like meaning.
func stringSlicesEqualUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
		if counts[v] < 0 {
			return false
		}
	}
	return true
}

// SetSummary writes summary + summary_updated_at on the documents row.
// Does NOT advance updated_at — the summary regen path runs after
// user-driven writes and would otherwise smear summary regen events
// over real edits.
func (s *Store) SetSummary(ctx context.Context, id, summary string, now time.Time) error {
	now = now.UTC()
	res, err := s.DB.ExecContext(ctx, `
        UPDATE documents SET summary = $1, summary_updated_at = $2 WHERE id = $3
    `, summary, now, id)
	if err != nil {
		return fmt.Errorf("document: set summary: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("document: set summary rows: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetByID returns the document row by id alone (no scope addressing).
// Used by the story read path, which addresses by id rather than
// (scope, name).
func (s *Store) GetByID(ctx context.Context, id string) (Document, error) {
	row := s.DB.QueryRowContext(ctx, selectDocumentColumns+` FROM documents WHERE id = $1`, id)
	return scanDocumentFull(row)
}

// GetByIDWithLatestBody is GetByID plus the body of the latest active
// version. Used by the story_get verb so callers see body inline.
func (s *Store) GetByIDWithLatestBody(ctx context.Context, id string) (Document, string, error) {
	doc, err := s.GetByID(ctx, id)
	if err != nil {
		return Document{}, "", err
	}
	row := s.DB.QueryRowContext(ctx, `
        SELECT body FROM document_versions
        WHERE document_id = $1 AND status = 'active'
        ORDER BY version DESC LIMIT 1
    `, id)
	var body string
	if err := row.Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return doc, "", nil
		}
		return Document{}, "", fmt.Errorf("document: get latest body: %w", err)
	}
	return doc, body, nil
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

// Delete soft-deletes a document by appending a tombstone version. This
// is the document_delete verb's path; stories use HardDelete instead.
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

	doc, v, err := appendVersion(ctx, tx, doc, "", deletedBy, now, StatusDeleted)
	if err != nil {
		return Document{}, Version{}, err
	}

	if err := tx.Commit(); err != nil {
		return Document{}, Version{}, fmt.Errorf("document: commit: %w", err)
	}
	return doc, v, nil
}

// ListStoriesByProject returns every type='story' document under the
// given project, ordered with the pre-unification story_list shape
// (status → priority → most-recent). The tag slice is treated as an
// AND filter when non-empty. This is the canonical story_list query;
// the generic List uses created_at DESC ordering for portable cursors.
func (s *Store) ListStoriesByProject(ctx context.Context, projectID string, tags []string) ([]Document, error) {
	q := selectDocumentColumns + `
        FROM documents
        WHERE type = 'story' AND project_id = $1`
	args := []any{projectID}
	if len(tags) > 0 {
		args = append(args, pq.Array(tags))
		q += ` AND tags @> $2`
	}
	q += `
        ORDER BY
            CASE status
                WHEN 'in_progress' THEN 0
                WHEN 'ready' THEN 1
                WHEN 'review' THEN 2
                WHEN 'backlog' THEN 3
                WHEN 'done' THEN 4
                WHEN 'cancelled' THEN 5
                ELSE 6
            END,
            CASE COALESCE(priority,'')
                WHEN 'critical' THEN 0
                WHEN 'high' THEN 1
                WHEN 'medium' THEN 2
                WHEN 'low' THEN 3
                ELSE 4
            END,
            updated_at DESC, id`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("document: list stories: %w", err)
	}
	defer rows.Close()
	out := []Document{}
	for rows.Next() {
		d, err := scanDocumentFull(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// HardDelete removes the documents row + cascades its versions. Used
// by the story_delete verb to preserve pre-unification story_delete
// semantics (hard removal).
func (s *Store) HardDelete(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM documents WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("document: hard delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("document: hard delete rows: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListFilter is the input shape for List. Every field is optional;
// Status/Type="all" or "" disables that predicate.
type ListFilter struct {
	Type        string   // "" or "all" → no filter; "document" / "story" → equality
	Scope       Scope    // "" → no filter
	WorkspaceID string   // "" → no filter
	ProjectID   string   // "" → no filter
	Tags        []string // empty → no filter; non-empty → AND filter via @>
	Status      string   // "" or "all" → no filter
	NamePrefix  string   // "" → no filter; non-empty → name ILIKE prefix||'%'
}

// ListOptions controls pagination. Limit defaults to 50, capped at 200.
// Cursor is the opaque value returned in the previous page's NextCursor.
type ListOptions struct {
	Limit  int
	Cursor string
}

// ListResult is the paged response from List.
type ListResult struct {
	Items      []Document
	NextCursor string
}

const (
	listDefaultLimit = 50
	listMaxLimit     = 200
)

// List returns documents matching the filter, ordered by created_at
// DESC then id DESC for cursor stability. NextCursor is non-empty iff
// the result was truncated at Limit.
func (s *Store) List(ctx context.Context, f ListFilter, opts ListOptions) (ListResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = listDefaultLimit
	}
	if limit > listMaxLimit {
		limit = listMaxLimit
	}

	var (
		preds []string
		args  []any
	)
	add := func(pred string, v any) {
		args = append(args, v)
		preds = append(preds, strings.Replace(pred, "?", fmt.Sprintf("$%d", len(args)), 1))
	}

	if f.Type != "" && f.Type != "all" {
		add("type = ?", f.Type)
	}
	if f.Scope != "" {
		add("scope = ?", string(f.Scope))
	}
	if f.WorkspaceID != "" {
		add("workspace_id = ?", f.WorkspaceID)
	}
	if f.ProjectID != "" {
		add("project_id = ?", f.ProjectID)
	}
	if len(f.Tags) > 0 {
		add("tags @> ?", pq.Array(f.Tags))
	}
	if f.Status != "" && f.Status != "all" {
		add("status = ?", f.Status)
	}
	if f.NamePrefix != "" {
		add("name ILIKE ?", f.NamePrefix+"%")
	}

	if opts.Cursor != "" {
		ts, id, err := decodeCursor(opts.Cursor)
		if err != nil {
			return ListResult{}, fmt.Errorf("document: list: %w", err)
		}
		args = append(args, ts, id)
		preds = append(preds, fmt.Sprintf("(created_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}

	q := selectDocumentColumns + " FROM documents"
	if len(preds) > 0 {
		q += " WHERE " + strings.Join(preds, " AND ")
	}
	q += " ORDER BY created_at DESC, id DESC"
	args = append(args, limit+1)
	q += fmt.Sprintf(" LIMIT $%d", len(args))

	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return ListResult{}, fmt.Errorf("document: list: %w", err)
	}
	defer rows.Close()

	out := make([]Document, 0, limit)
	for rows.Next() {
		d, err := scanDocumentFull(rows)
		if err != nil {
			return ListResult{}, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, fmt.Errorf("document: list rows: %w", err)
	}

	var next string
	if len(out) > limit {
		last := out[limit-1]
		next = encodeCursor(last.CreatedAt, last.ID)
		out = out[:limit]
	}
	return ListResult{Items: out, NextCursor: next}, nil
}

// Count returns the number of rows matching the filter. Mirrors List's
// WHERE clause exactly so the count is consistent with what a cursor
// walk would surface across pages.
func (s *Store) Count(ctx context.Context, f ListFilter) (int, error) {
	var (
		preds []string
		args  []any
	)
	add := func(pred string, v any) {
		args = append(args, v)
		preds = append(preds, strings.Replace(pred, "?", fmt.Sprintf("$%d", len(args)), 1))
	}

	if f.Type != "" && f.Type != "all" {
		add("type = ?", f.Type)
	}
	if f.Scope != "" {
		add("scope = ?", string(f.Scope))
	}
	if f.WorkspaceID != "" {
		add("workspace_id = ?", f.WorkspaceID)
	}
	if f.ProjectID != "" {
		add("project_id = ?", f.ProjectID)
	}
	if len(f.Tags) > 0 {
		add("tags @> ?", pq.Array(f.Tags))
	}
	if f.Status != "" && f.Status != "all" {
		add("status = ?", f.Status)
	}
	if f.NamePrefix != "" {
		add("name ILIKE ?", f.NamePrefix+"%")
	}

	q := "SELECT COUNT(*) FROM documents"
	if len(preds) > 0 {
		q += " WHERE " + strings.Join(preds, " AND ")
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("document: count: %w", err)
	}
	return n, nil
}

func encodeCursor(t time.Time, id string) string {
	raw := fmt.Sprintf("%s|%s", t.UTC().Format(time.RFC3339Nano), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor encoding")
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("invalid cursor shape")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor timestamp")
	}
	return t, parts[1], nil
}

// appendVersion adds the next document_versions row + bumps the
// documents.latest_version pointer. Shared between Upsert and Delete.
func appendVersion(ctx context.Context, tx *sql.Tx, doc Document, body, by string, now time.Time, status Status) (Document, Version, error) {
	next := doc.LatestVersion + 1
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO document_versions (document_id, version, body, status, created_at, created_by)
        VALUES ($1, $2, $3, $4, $5, $6)
    `, doc.ID, next, body, string(status), now, by); err != nil {
		return Document{}, Version{}, fmt.Errorf("document: insert version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
        UPDATE documents SET latest_version = $1, updated_at = $2 WHERE id = $3
    `, next, now, doc.ID); err != nil {
		return Document{}, Version{}, fmt.Errorf("document: update latest: %w", err)
	}
	doc.LatestVersion = next
	doc.UpdatedAt = now
	return doc, Version{
		DocumentID: doc.ID,
		Version:    next,
		Body:       body,
		Status:     status,
		CreatedAt:  now,
		CreatedBy:  by,
	}, nil
}

// lockOrInsertDocument resolves the documents row for an upsert. The
// row's type is set on first insert from docType (document or skill);
// subsequent calls return the existing row regardless of its type so
// the caller can detect type mismatches and reject them at the Upsert
// boundary.
func lockOrInsertDocument(ctx context.Context, tx *sql.Tx, key Key, docType string, now time.Time) (Document, error) {
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
            (id, scope, workspace_id, project_id, name, latest_version,
             type, tags, status, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, 0,
                $7, '{}', 'active', $6, $6)
    `, id, string(key.Scope), wsArg, pjArg, key.Name, now, docType); err != nil {
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
	row := tx.QueryRowContext(ctx, selectDocumentColumns+`
        FROM documents
        WHERE scope = $1
          AND workspace_id IS NOT DISTINCT FROM $2
          AND project_id   IS NOT DISTINCT FROM $3
          AND name = $4
          AND type IN ('document','skill')
        FOR UPDATE
    `, string(key.Scope), wsArg, pjArg, key.Name)
	return scanDocumentFull(row)
}

func lockDocumentByID(ctx context.Context, tx *sql.Tx, id string) (Document, error) {
	row := tx.QueryRowContext(ctx, selectDocumentColumns+` FROM documents WHERE id = $1 FOR UPDATE`, id)
	return scanDocumentFull(row)
}

func (s *Store) lookupDocument(ctx context.Context, key Key) (Document, error) {
	wsArg := nullStr(key.WorkspaceID)
	pjArg := nullStr(key.ProjectID)
	row := s.DB.QueryRowContext(ctx, selectDocumentColumns+`
        FROM documents
        WHERE scope = $1
          AND workspace_id IS NOT DISTINCT FROM $2
          AND project_id   IS NOT DISTINCT FROM $3
          AND name = $4
          AND type IN ('document','skill')
    `, string(key.Scope), wsArg, pjArg, key.Name)
	return scanDocumentFull(row)
}

func (s *Store) getVersion(ctx context.Context, docID string, version int, includeDeleted bool) (Version, error) {
	if version == 0 {
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

const selectDocumentColumns = `SELECT
    id, type, scope,
    COALESCE(workspace_id,''), COALESCE(project_id,''),
    name, latest_version,
    tags, status,
    COALESCE(priority,''), COALESCE(category,''),
    COALESCE(parent_id,''), acceptance_criteria,
    summary, summary_updated_at,
    created_at, updated_at`

func scanDocumentFull(rs rowScanner) (Document, error) {
	var (
		d         Document
		scopeS    string
		tags      pq.StringArray
		summaryAt sql.NullTime
	)
	if err := rs.Scan(&d.ID, &d.Type, &scopeS,
		&d.WorkspaceID, &d.ProjectID,
		&d.Name, &d.LatestVersion,
		&tags, &d.Status,
		&d.Priority, &d.Category,
		&d.ParentID, &d.AcceptanceCriteria,
		&d.Summary, &summaryAt,
		&d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Document{}, ErrNotFound
		}
		return Document{}, fmt.Errorf("document: scan: %w", err)
	}
	d.Scope = Scope(scopeS)
	d.Tags = []string(tags)
	if d.Tags == nil {
		d.Tags = []string{}
	}
	if summaryAt.Valid {
		t := summaryAt.Time
		d.SummaryUpdatedAt = &t
	}
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

// newStoryID returns a fresh story id in the `sty_<8hex>` form. The
// id format predates unification; documents.id accepts either
// `doc_…` or `sty_…` so existing evidence.story_id values keep
// resolving by exact match.
func newStoryID() string {
	id := NewID() // doc_<hex>
	return "sty_" + id[len("doc_"):]
}

// newTaskID returns a fresh task id in the `tsk_<8hex>` form, matching
// the per-type prefix convention (doc_, sty_, tsk_).
func newTaskID() string {
	id := NewID()
	return "tsk_" + id[len("doc_"):]
}

// CreateTaskInput is the write payload for CreateTask. ParentID is
// required — every task belongs to a story. Optional fields fall back
// to substrate defaults: status='planned', priority='medium', tags=[].
type CreateTaskInput struct {
	ProjectID   string
	WorkspaceID string
	ParentID    string // must reference a type='story' row
	Title       string
	Body        string
	Status      string
	Priority    string
	Tags        []string
	CreatedBy   string
}

// CreateTask inserts a new type='task' documents row plus its v1
// document_versions body. The parent story is verified inside the same
// transaction so a deleted story can't orphan tasks via a TOCTOU race.
func (s *Store) CreateTask(ctx context.Context, in CreateTaskInput, now time.Time) (Document, error) {
	if in.ProjectID == "" {
		return Document{}, fmt.Errorf("document: project_id required")
	}
	if in.WorkspaceID == "" {
		return Document{}, fmt.Errorf("document: workspace_id required")
	}
	if in.ParentID == "" {
		return Document{}, fmt.Errorf("document: parent_id required for task")
	}
	if in.Title == "" {
		return Document{}, fmt.Errorf("document: title required")
	}
	if in.Status == "" {
		in.Status = "planned"
	}
	if in.Priority == "" {
		in.Priority = "medium"
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	now = now.UTC()
	id := newTaskID()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, fmt.Errorf("document: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Verify parent is a story under the same project — a task can't
	// belong to a document, another task, or a story in a different
	// project. The DB-level parent_id FK only checks the row exists;
	// this query enforces the semantic constraint.
	var parentType, parentProjectID string
	if err := tx.QueryRowContext(ctx,
		`SELECT type, COALESCE(project_id,'') FROM documents WHERE id = $1`, in.ParentID,
	).Scan(&parentType, &parentProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Document{}, fmt.Errorf("document: parent_id %q not found: %w", in.ParentID, ErrNotFound)
		}
		return Document{}, fmt.Errorf("document: parent lookup: %w", err)
	}
	if parentType != TypeStory {
		return Document{}, fmt.Errorf("document: task parent must be a story, got type=%s", parentType)
	}
	if parentProjectID != in.ProjectID {
		return Document{}, fmt.Errorf("document: task project_id must match parent story project_id")
	}

	if _, err := tx.ExecContext(ctx, `
        INSERT INTO documents
            (id, scope, workspace_id, project_id, name, latest_version,
             type, tags, status, priority, parent_id,
             created_at, updated_at)
        VALUES ($1, 'project', $2, $3, $4, 1,
                'task', $5, $6, $7, $8,
                $9, $9)
    `, id, in.WorkspaceID, in.ProjectID, in.Title,
		pq.Array(in.Tags), in.Status, in.Priority, in.ParentID,
		now); err != nil {
		return Document{}, fmt.Errorf("document: insert task: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO document_versions (document_id, version, body, status, created_at, created_by)
        VALUES ($1, 1, $2, 'active', $3, $4)
    `, id, in.Body, now, in.CreatedBy); err != nil {
		return Document{}, fmt.Errorf("document: insert task v1: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Document{}, fmt.Errorf("document: commit task: %w", err)
	}

	return Document{
		ID: id, Type: TypeTask, Scope: ScopeProject,
		WorkspaceID: in.WorkspaceID, ProjectID: in.ProjectID,
		Name: in.Title, LatestVersion: 1,
		Tags: in.Tags, Status: in.Status,
		Priority: in.Priority, ParentID: in.ParentID,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}
