// Package blob is the binary-ingestion store (sty_59652a7d): document-
// collection projects accept binary uploads (PDF, decks, images) that cannot
// ride an MCP tool call. Content lands in Postgres bytea — satellites-native
// storage; an object store (S3/Tigris) is a later swap behind this same seam.
// The extractor reads a blob and materialises a text-bodied document that
// references it.
package blob

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a blob lookup misses.
var ErrNotFound = errors.New("blob: not found")

// Blob is one stored upload's metadata (content fetched separately).
type Blob struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	ProjectID   string    `json:"project_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   string    `json:"created_by,omitempty"`
}

// Store wraps DB-backed blob operations (migration 0032).
type Store struct{ DB *sql.DB }

// New returns a Store bound to db.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// NewID returns a fresh blob id in the canonical blob_<8hex> form.
func NewID() string { return fmt.Sprintf("blob_%s", uuid.NewString()[:8]) }

// CreateInput carries a new blob's metadata; the content is passed alongside.
type CreateInput struct {
	WorkspaceID string
	ProjectID   string
	Filename    string
	ContentType string
	CreatedBy   string
}

// Create stores content as a new blob, computing its sha256 and size, and
// returns the persisted metadata. workspace_id, project_id, and non-empty
// content are required — validated before any DB use.
func (s *Store) Create(ctx context.Context, in CreateInput, content []byte, now time.Time) (Blob, error) {
	if in.WorkspaceID == "" || in.ProjectID == "" {
		return Blob{}, fmt.Errorf("blob: workspace_id and project_id required")
	}
	if len(content) == 0 {
		return Blob{}, fmt.Errorf("blob: empty content")
	}
	sum := sha256.Sum256(content)
	b := Blob{
		ID:          NewID(),
		WorkspaceID: in.WorkspaceID,
		ProjectID:   in.ProjectID,
		Filename:    in.Filename,
		ContentType: in.ContentType,
		SizeBytes:   int64(len(content)),
		SHA256:      hex.EncodeToString(sum[:]),
		CreatedAt:   now.UTC(),
		CreatedBy:   in.CreatedBy,
	}
	if _, err := s.DB.ExecContext(ctx, `
        INSERT INTO blobs
            (id, workspace_id, project_id, filename, content_type,
             size_bytes, sha256, content, created_at, created_by)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
    `, b.ID, b.WorkspaceID, b.ProjectID, b.Filename, b.ContentType,
		b.SizeBytes, b.SHA256, content, b.CreatedAt, b.CreatedBy); err != nil {
		return Blob{}, fmt.Errorf("blob: insert: %w", err)
	}
	return b, nil
}

// GetContent returns a blob's metadata and raw bytes, or ErrNotFound. The
// extractor uses this to read an uploaded file server-side.
func (s *Store) GetContent(ctx context.Context, id string) (Blob, []byte, error) {
	var (
		b       Blob
		content []byte
	)
	row := s.DB.QueryRowContext(ctx, `
        SELECT id, workspace_id, project_id, filename, content_type,
               size_bytes, sha256, content, created_at, created_by
        FROM blobs WHERE id = $1
    `, id)
	if err := row.Scan(&b.ID, &b.WorkspaceID, &b.ProjectID, &b.Filename,
		&b.ContentType, &b.SizeBytes, &b.SHA256, &content, &b.CreatedAt, &b.CreatedBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Blob{}, nil, ErrNotFound
		}
		return Blob{}, nil, fmt.Errorf("blob: get: %w", err)
	}
	return b, content, nil
}
