package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/verb"
)

// maxBlobUploadBytes caps a single upload (prototype guard). 32 MiB covers the
// engagement's PDFs/decks; larger assets wait on the object-store backend.
const maxBlobUploadBytes = 32 << 20

// BlobUpload is what the transport hands the injected StoreBlob func. The blob
// substrate store stays out of this package (layering guard), so the server
// speaks only primitives + this struct; main wires the concrete store.
type BlobUpload struct {
	WorkspaceID string
	ProjectID   string
	Filename    string
	ContentType string
	CreatedBy   string
	Content     []byte
}

// BlobRef is the stored-blob reference returned to the caller — the handle a
// document later points at.
type BlobRef struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
}

// StoreBlobFunc persists an uploaded blob and returns its reference. Injected
// from main (which owns internal/blob) so this transport package imports no
// substrate store. A nil StoreBlob disables the upload route.
type StoreBlobFunc func(ctx context.Context, up BlobUpload) (BlobRef, error)

// blobUploadHandler serves POST /projects/{id}/blobs — a multipart binary
// upload to a project the caller can access. Mounted behind the same Bearer
// middleware as /mcp. Project access + workspace resolution reuse project_get,
// so a forbidden/not-found caller never reaches the store.
func blobUploadHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.FromContext(ctx)
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		projectID := r.PathValue("id")
		if projectID == "" {
			http.Error(w, "project id required", http.StatusBadRequest)
			return
		}

		// Access check + workspace resolution in one call: project_get enforces
		// read access and returns the project row (incl. workspace_id).
		getReq, _ := json.Marshal(verb.ProjectGetRequest{ID: projectID})
		raw, err := verb.Dispatch(ctx, "project_get", getReq)
		if err != nil {
			arbor.WarnCtx(ctx, "blob upload: project access", "project_id", projectID, "err", err)
			http.Error(w, "no access to project", http.StatusForbidden)
			return
		}
		var pg struct {
			WorkspaceID string `json:"workspace_id"`
		}
		_ = json.Unmarshal(raw, &pg)

		r.Body = http.MaxBytesReader(w, r.Body, maxBlobUploadBytes)
		if err := r.ParseMultipartForm(maxBlobUploadBytes); err != nil {
			http.Error(w, "upload too large or malformed multipart form", http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing 'file' form field", http.StatusBadRequest)
			return
		}
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "read upload", http.StatusBadRequest)
			return
		}
		if len(content) == 0 {
			http.Error(w, "empty file", http.StatusBadRequest)
			return
		}

		ref, err := cfg.StoreBlob(ctx, BlobUpload{
			WorkspaceID: pg.WorkspaceID,
			ProjectID:   projectID,
			Filename:    header.Filename,
			ContentType: header.Header.Get("Content-Type"),
			CreatedBy:   user.ID,
			Content:     content,
		})
		if err != nil {
			arbor.ErrorCtx(ctx, "blob upload: store", "project_id", projectID, "err", err)
			http.Error(w, "store blob", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(ref)
	}
}
