package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/extract"
)

// workspaceDocumentUploadHandler serves POST /workspaces/{id}/documents — the
// PORTAL's add-document control (drag-drop / browse) for the workspace corpus
// (sty_5d97b972). Unlike POST /workspaces/{id}/blobs (Bearer-gated), this route
// authenticates via the session cookie like the other portal pages, so the
// browser fetch from the workspace detail page is authorised. It reuses the
// SAME ingestion path (cfg.StoreBlob → ingest.StoreBlobAndExtract) — no parallel
// upload logic. Access is global-admin OR workspace member; a non-member is
// refused, fail-closed.
func workspaceDocumentUploadHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Error(w, "not signed in", http.StatusUnauthorized)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)

		wsID := r.PathValue("id")
		if wsID == "" {
			http.Error(w, "workspace id required", http.StatusBadRequest)
			return
		}
		if !callerCanAccessWorkspace(ctx, wsID) {
			http.Error(w, "you are not a member of this workspace", http.StatusForbidden)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxBlobUploadBytes)
		if err := r.ParseMultipartForm(maxBlobUploadBytes); err != nil {
			http.Error(w, "upload is too large or the form is malformed (32 MiB max)", http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "choose a file to upload", http.StatusBadRequest)
			return
		}
		defer file.Close()

		contentType := header.Header.Get("Content-Type")
		if !extract.Supported(header.Filename, contentType) {
			http.Error(w, "unsupported file type — upload a PDF or a text/markdown document", http.StatusBadRequest)
			return
		}

		content, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "could not read the upload", http.StatusBadRequest)
			return
		}
		if len(content) == 0 {
			http.Error(w, "the file is empty", http.StatusBadRequest)
			return
		}

		ref, err := cfg.StoreBlob(ctx, BlobUpload{
			WorkspaceID: wsID,
			// ProjectID empty → workspace corpus (matches the documents panel).
			Filename:    header.Filename,
			ContentType: contentType,
			CreatedBy:   userID,
			Content:     content,
		})
		if err != nil {
			arbor.ErrorCtx(ctx, "workspace document upload: store", "workspace_id", wsID, "err", err)
			http.Error(w, "could not store the document", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(ref)
	}
}
