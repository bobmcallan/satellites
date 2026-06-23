// Package ingest is the shared blob-ingestion path: persist an uploaded binary
// and, for an extractable type, materialise a text document that points back at
// the blob. It is the single home for the logic that used to live inline in
// cmd/satellites-server's StoreBlob closure (sty_3c2f02bf), so the server and
// the integration tests exercise the identical path.
//
// The only difference between the two ingestion modes is scope, decided by
// whether a project is named:
//   - ProjectID set   → repo attachment: project-scoped blob + document.
//   - ProjectID empty → workspace corpus: workspace-scoped blob + document.
package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/blob"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/extract"
)

// Upload is a binary to persist + extract. ProjectID empty ⇒ the blob and its
// extracted document belong to the workspace alone (the engagement corpus).
type Upload struct {
	WorkspaceID string
	ProjectID   string
	Filename    string
	ContentType string
	CreatedBy   string
	Content     []byte
}

// Ref is the stored-blob reference, with the extracted document id + flag when
// the type was extractable (image/octet-stream → Extracted false, blob kept).
type Ref struct {
	ID          string
	WorkspaceID string
	ProjectID   string
	Filename    string
	ContentType string
	SizeBytes   int64
	SHA256      string
	DocumentID  string
	Extracted   bool
}

// StoreBlobAndExtract persists up as a blob and, for an extractable type,
// materialises a text document that references it. The document is
// workspace-scoped when up.ProjectID is empty, else project-scoped — the only
// difference between the corpus and repo-attachment paths. Extraction is
// best-effort: a failure never fails the upload (the blob is already stored).
func StoreBlobAndExtract(ctx context.Context, blobStore *blob.Store, docStore *document.Store, up Upload) (Ref, error) {
	b, err := blobStore.Create(ctx, blob.CreateInput{
		WorkspaceID: up.WorkspaceID,
		ProjectID:   up.ProjectID,
		Filename:    up.Filename,
		ContentType: up.ContentType,
		CreatedBy:   up.CreatedBy,
	}, up.Content, time.Now().UTC())
	if err != nil {
		return Ref{}, err
	}
	ref := Ref{
		ID: b.ID, WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID,
		Filename: b.Filename, ContentType: b.ContentType,
		SizeBytes: b.SizeBytes, SHA256: b.SHA256,
	}

	text, ok := extract.Text(up.Filename, up.ContentType, up.Content)
	if !ok {
		return ref, nil
	}

	// Workspace-corpus blob (no project) → workspace-scoped document; repo
	// attachment → project-scoped document. The origin pointer matches the
	// download route the blob is reachable through.
	key := document.Key{Scope: document.ScopeProject, WorkspaceID: up.WorkspaceID, ProjectID: up.ProjectID, Name: "attachment-" + b.ID}
	origin := fmt.Sprintf("GET /projects/%s/blobs/%s", b.ProjectID, b.ID)
	if up.ProjectID == "" {
		key.Scope = document.ScopeWorkspace
		origin = fmt.Sprintf("GET /workspaces/%s/blobs/%s", b.WorkspaceID, b.ID)
	}
	body := fmt.Sprintf("# %s\n\n> Extracted from attachment `%s` (%s, %d bytes). Original: %s\n\n%s",
		b.Filename, b.ID, b.ContentType, b.SizeBytes, origin, text)
	doc, _, derr := docStore.Upsert(ctx, document.UpsertInput{
		Key:       key,
		Type:      document.TypeDocument,
		Body:      body,
		CreatedBy: up.CreatedBy,
	}, time.Now().UTC())
	if derr != nil {
		// Best-effort: the blob is retained; surface the doc failure and return.
		arbor.WarnCtx(ctx, "ingest: create extracted document", "blob_id", b.ID, "err", derr)
		return ref, nil
	}
	// Classify the extracted document as type:document (the KV classification the
	// documents panels filter on) so an uploaded doc is a first-class project /
	// workspace document and appears in the panel — agent-authored docs already
	// carry this tag (epic:phases-task-outputs). Best-effort, like the doc create.
	if _, terr := docStore.SetDocumentTags(ctx, doc.ID, []string{"type:document"}, time.Now().UTC()); terr != nil {
		arbor.WarnCtx(ctx, "ingest: tag extracted document", "doc_id", doc.ID, "err", terr)
	}
	ref.DocumentID = doc.ID
	ref.Extracted = true
	return ref, nil
}
