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
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

	text, hasText := extract.Text(up.Filename, up.ContentType, up.Content)
	// An image is an accepted upload with no extractable text (sty_49a3762e): it
	// still becomes a metadata-only document so it appears in the documents panel.
	// A truly-unsupported/unreadable input (no text, not an image) keeps the blob
	// but creates no document row.
	isImage := extract.IsImage(up.Filename, up.ContentType)
	if !hasText && !isImage {
		return ref, nil
	}

	// Workspace-corpus blob (no project) → workspace-scoped document; repo
	// attachment → project-scoped document. The origin pointer matches the
	// download route the blob is reachable through.
	key := document.Key{Scope: document.ScopeProject, WorkspaceID: up.WorkspaceID, ProjectID: up.ProjectID, Name: docName(b.Filename, b.ID)}
	origin := fmt.Sprintf("GET /projects/%s/blobs/%s", b.ProjectID, b.ID)
	if up.ProjectID == "" {
		key.Scope = document.ScopeWorkspace
		origin = fmt.Sprintf("GET /workspaces/%s/blobs/%s", b.WorkspaceID, b.ID)
	}
	// Disambiguate against an existing document at the derived name so an upload
	// never appends a version to — and silently supersedes — an unrelated
	// document that happens to share the filename stem (sty_c62e9b63 AC4).
	resolved, kerr := uniqueKey(ctx, docStore, key)
	if kerr != nil {
		arbor.WarnCtx(ctx, "ingest: resolve unique document name", "blob_id", b.ID, "err", kerr)
		return ref, nil
	}
	key = resolved
	// Text uploads embed the extracted text; an image carries none, so its body is
	// the metadata header alone ("Uploaded attachment" rather than "Extracted from").
	lead := "Extracted from attachment"
	if !hasText {
		lead = "Uploaded attachment"
	}
	body := fmt.Sprintf("# %s\n\n> %s `%s` (%s, %d bytes). Original: %s",
		b.Filename, lead, b.ID, b.ContentType, b.SizeBytes, origin)
	if hasText {
		body += "\n\n" + text
	}
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
	// carry this tag (epic:phases-task-outputs). The source:upload tag records
	// portal-upload provenance so an upload is distinguishable from an
	// agent-authored doc (sty_c62e9b63). Best-effort, like the doc create.
	if _, terr := docStore.SetDocumentTags(ctx, doc.ID, []string{"type:document", "source:upload"}, time.Now().UTC()); terr != nil {
		arbor.WarnCtx(ctx, "ingest: tag extracted document", "doc_id", doc.ID, "err", terr)
	}
	ref.DocumentID = doc.ID
	ref.Extracted = true
	return ref, nil
}

// docName derives a document name from the upload filename: the basename with
// its extension stripped (architecture-as-built-2014.md →
// architecture-as-built-2014), matching the manifest naming convention. Falls
// back to the blob-id name when the filename yields nothing usable (empty,
// extension-only such as a dotfile, or a bare separator).
func docName(filename, blobID string) string {
	base := strings.TrimSpace(filepath.Base(filename))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "attachment-" + blobID
	}
	return base
}

// uniqueKey returns key unchanged when no document exists at its name, else key
// with a numeric suffix (-2, -3, …) pointing at the first free slot. Documents
// are key-addressed per (scope, workspace, project, name); without this an
// upload whose filename stem collides with an existing document would append a
// version to that document and supersede it (sty_c62e9b63 AC4). A soft-deleted
// name is treated as free — reusing a tombstoned name is intended.
func uniqueKey(ctx context.Context, docStore *document.Store, key document.Key) (document.Key, error) {
	base := key.Name
	for i := 1; i <= 1000; i++ {
		if i > 1 {
			key.Name = fmt.Sprintf("%s-%d", base, i)
		}
		_, err := docStore.Get(ctx, key, document.GetOptions{})
		if errors.Is(err, document.ErrNotFound) {
			return key, nil
		}
		if err != nil {
			return document.Key{}, err
		}
	}
	return document.Key{}, fmt.Errorf("ingest: no free document name for %q after 1000 tries", base)
}
