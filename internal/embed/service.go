package embed

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// Service ties the embedder, chunker, and chunk store to the document/workspace
// stores: embed a document, drop its chunks, search a workspace corpus, and
// reconcile (the worker's unit of work). Constructed with a nil embedder when no
// Gemini key is present, in which case embedding is a graceful no-op (AC3).
type Service struct {
	embedder Embedder
	chunks   *ChunkStore
	docs     *document.Store
	ws       *workspace.Store
	chunker  Chunker
}

// NewService wires the service. chunkMaxTokens/chunkOverlap are configuration
// (AC3); zero values fall back to the chunker's 512/64 defaults.
func NewService(embedder Embedder, chunks *ChunkStore, docs *document.Store, ws *workspace.Store, chunkMaxTokens, chunkOverlap int) *Service {
	return &Service{
		embedder: embedder,
		chunks:   chunks,
		docs:     docs,
		ws:       ws,
		chunker:  Chunker{MaxTokens: chunkMaxTokens, Overlap: chunkOverlap},
	}
}

// Enabled reports whether an embedder is wired (a key was supplied).
func (s *Service) Enabled() bool { return s.embedder != nil }

// EmbedDocument chunks text, embeds the chunks with Gemini, and replaces the
// document's stored chunk set (stamped with docVersion so a re-embed supersedes
// the prior set). With no embedder it is a logged no-op — the corpus is stored
// unembedded, never failing ingest (AC3).
func (s *Service) EmbedDocument(ctx context.Context, workspaceID, documentID string, docVersion int, text string) error {
	if s.embedder == nil {
		arbor.WarnCtx(ctx, "embed: skipped, no embedder (GEMINI_API_KEY unset)", "document_id", documentID)
		return nil
	}
	pieces := s.chunker.Chunk(text)
	if len(pieces) == 0 {
		return s.chunks.DeleteDocumentChunks(ctx, documentID)
	}
	texts := make([]string, len(pieces))
	for i, p := range pieces {
		texts[i] = p.Content
	}
	vecs, err := s.embedder.Embed(ctx, texts)
	if err != nil {
		return err
	}
	if len(vecs) != len(pieces) {
		return fmt.Errorf("embed: %d vectors for %d chunks", len(vecs), len(pieces))
	}
	stored := make([]storedChunk, len(pieces))
	for i, p := range pieces {
		stored[i] = storedChunk{Index: p.Index, Content: p.Content, TokenCount: p.TokenCount, Embedding: float32sToFloat64s(vecs[i])}
	}
	return s.chunks.ReplaceDocumentChunks(ctx, workspaceID, documentID, docVersion, s.embedder.Dimension(), stored, time.Now().UTC())
}

// RemoveDocument drops a document's chunks (AC2).
func (s *Service) RemoveDocument(ctx context.Context, documentID string) error {
	return s.chunks.DeleteDocumentChunks(ctx, documentID)
}

// Search embeds the query and returns the top-k workspace chunks by cosine
// similarity, each carrying document provenance (AC4 — the internal API
// epic-order:5 consumes). With no embedder or an empty query it returns an empty
// result, never an error (non-fatal).
func (s *Service) Search(ctx context.Context, workspaceID, query string, k int) ([]ScoredChunk, error) {
	if s.embedder == nil || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	vecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, nil
	}
	candidates, err := s.chunks.WorkspaceChunks(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return topK(float32sToFloat64s(vecs[0]), candidates, k), nil
}

// Reconcile embeds workspace documents whose chunk set is missing or behind
// their latest version, and prunes chunks whose document is gone — the
// background worker's unit of work (AC1). Best-effort per document/workspace: a
// failure is logged and the loop continues.
func (s *Service) Reconcile(ctx context.Context) error {
	if s.embedder == nil {
		return nil
	}
	wss, err := s.ws.List(ctx)
	if err != nil {
		return err
	}
	for _, w := range wss {
		s.reconcileWorkspace(ctx, w.ID)
	}
	return nil
}

func (s *Service) reconcileWorkspace(ctx context.Context, wsID string) {
	res, err := s.docs.List(ctx,
		document.ListFilter{Type: "document", Scope: document.ScopeWorkspace, WorkspaceID: wsID},
		document.ListOptions{Limit: 500})
	if err != nil {
		arbor.WarnCtx(ctx, "embed: reconcile list", "workspace_id", wsID, "err", err)
		return
	}
	live := map[string]bool{}
	for _, d := range res.Items {
		live[d.ID] = true
		cur, ok, err := s.chunks.DocVersion(ctx, d.ID)
		if err != nil {
			arbor.WarnCtx(ctx, "embed: reconcile docversion", "document_id", d.ID, "err", err)
			continue
		}
		if ok && cur >= d.LatestVersion {
			continue // chunks are current
		}
		got, err := s.docs.Get(ctx, document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: d.Name}, document.GetOptions{})
		if err != nil || len(got.Versions) == 0 {
			arbor.WarnCtx(ctx, "embed: reconcile get body", "document_id", d.ID, "err", err)
			continue
		}
		if err := s.EmbedDocument(ctx, wsID, d.ID, d.LatestVersion, got.Versions[0].Body); err != nil {
			arbor.WarnCtx(ctx, "embed: reconcile embed", "document_id", d.ID, "err", err)
		}
	}
	// Prune chunks for documents no longer present (tombstoned / hard-deleted).
	ids, err := s.chunks.WorkspaceDocIDs(ctx, wsID)
	if err != nil {
		arbor.WarnCtx(ctx, "embed: reconcile prune list", "workspace_id", wsID, "err", err)
		return
	}
	for _, id := range ids {
		if !live[id] {
			if err := s.chunks.DeleteDocumentChunks(ctx, id); err != nil {
				arbor.WarnCtx(ctx, "embed: reconcile prune", "document_id", id, "err", err)
			}
		}
	}
}
