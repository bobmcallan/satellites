package embed

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ScoredChunk is a stored chunk with its embedding and (after ranking) cosine
// score — the provenance unit a search returns (sty_7f4f7e11).
type ScoredChunk struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	DocumentID  string    `json:"document_id"`
	ChunkIndex  int       `json:"chunk_index"`
	Content     string    `json:"content"`
	Embedding   []float64 `json:"-"`
	Score       float64   `json:"score"`
}

// ChunkStore is the Postgres-backed document_chunks store (migration 0038).
type ChunkStore struct{ DB *sql.DB }

// NewChunkStore binds a store to db.
func NewChunkStore(db *sql.DB) *ChunkStore { return &ChunkStore{DB: db} }

func newChunkID() string { return fmt.Sprintf("chk_%s", uuid.NewString()[:8]) }

// storedChunk is one chunk to persist (content + embedding + index/tokens).
type storedChunk struct {
	Index      int
	Content    string
	TokenCount int
	Embedding  []float64
}

// ReplaceDocumentChunks atomically swaps a document's chunk set for the given
// chunks, stamping doc_version so the reconcile loop can detect staleness. A
// re-embed (changed document) thus replaces prior chunks rather than appending.
func (s *ChunkStore) ReplaceDocumentChunks(ctx context.Context, workspaceID, documentID string, docVersion, dim int, chunks []storedChunk, now time.Time) error {
	if workspaceID == "" || documentID == "" {
		return fmt.Errorf("embed: workspace_id and document_id required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("embed: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_chunks WHERE document_id = $1`, documentID); err != nil {
		return fmt.Errorf("embed: clear chunks: %w", err)
	}
	for _, c := range chunks {
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO document_chunks
                (id, workspace_id, document_id, chunk_index, content, token_count, embedding, dim, doc_version, created_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
        `, newChunkID(), workspaceID, documentID, c.Index, c.Content, c.TokenCount,
			pq.Array(c.Embedding), dim, docVersion, now.UTC()); err != nil {
			return fmt.Errorf("embed: insert chunk: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("embed: commit: %w", err)
	}
	return nil
}

// DeleteDocumentChunks removes all chunks for a document (idempotent).
func (s *ChunkStore) DeleteDocumentChunks(ctx context.Context, documentID string) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM document_chunks WHERE document_id = $1`, documentID); err != nil {
		return fmt.Errorf("embed: delete chunks: %w", err)
	}
	return nil
}

// DocVersion returns the doc_version stamped on a document's chunks, and whether
// any chunk exists. The reconcile loop re-embeds when this is absent or behind
// the document's latest_version.
func (s *ChunkStore) DocVersion(ctx context.Context, documentID string) (int, bool, error) {
	var v sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT MAX(doc_version) FROM document_chunks WHERE document_id = $1`, documentID).Scan(&v)
	if err != nil {
		return 0, false, fmt.Errorf("embed: doc version: %w", err)
	}
	if !v.Valid {
		return 0, false, nil
	}
	return int(v.Int64), true, nil
}

// WorkspaceChunks loads every chunk (with embedding) for a workspace — the
// brute-force search candidate set.
func (s *ChunkStore) WorkspaceChunks(ctx context.Context, workspaceID string) ([]ScoredChunk, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, workspace_id, document_id, chunk_index, content, embedding
        FROM document_chunks WHERE workspace_id = $1
    `, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("embed: list workspace chunks: %w", err)
	}
	defer rows.Close()
	var out []ScoredChunk
	for rows.Next() {
		var (
			c   ScoredChunk
			emb pq.Float64Array
		)
		if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.DocumentID, &c.ChunkIndex, &c.Content, &emb); err != nil {
			return nil, fmt.Errorf("embed: scan chunk: %w", err)
		}
		c.Embedding = []float64(emb)
		out = append(out, c)
	}
	return out, rows.Err()
}

// WorkspaceDocIDs returns the distinct document ids that currently have chunks
// in a workspace — the prune candidate set for the reconcile loop.
func (s *ChunkStore) WorkspaceDocIDs(ctx context.Context, workspaceID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT document_id FROM document_chunks WHERE workspace_id = $1`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("embed: workspace doc ids: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("embed: scan doc id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
