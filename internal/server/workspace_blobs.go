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

// workspaceBlobUploadHandler serves POST /workspaces/{id}/blobs — a multipart
// upload into the workspace corpus (no project). Bearer-gated like the project
// route; access is gated by callerCanAccessWorkspace (global admin or member),
// so a non-member never reaches the store. The injected StoreBlob persists the
// blob and extracts a workspace-scoped document (ProjectID empty). sty_3c2f02bf.
func workspaceBlobUploadHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.FromContext(ctx)
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		wsID := r.PathValue("id")
		if wsID == "" {
			http.Error(w, "workspace id required", http.StatusBadRequest)
			return
		}
		if !callerCanAccessWorkspace(ctx, wsID) {
			http.Error(w, "no access to workspace", http.StatusForbidden)
			return
		}

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
			WorkspaceID: wsID,
			// ProjectID empty → the ingest path keys the blob + extracted
			// document to the workspace corpus.
			Filename:    header.Filename,
			ContentType: header.Header.Get("Content-Type"),
			CreatedBy:   user.ID,
			Content:     content,
		})
		if err != nil {
			arbor.ErrorCtx(ctx, "workspace blob upload: store", "workspace_id", wsID, "err", err)
			http.Error(w, "store blob", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(ref)
	}
}

// workspaceBlobDownloadHandler serves GET /workspaces/{id}/blobs/{blob} —
// streams a workspace-corpus blob's original bytes, behind the same Bearer auth
// + workspace access check as upload. A blob whose workspace_id does not match
// the path (or is missing) is 404.
func workspaceBlobDownloadHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if auth.FromContext(ctx) == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		wsID := r.PathValue("id")
		blobID := r.PathValue("blob")
		if wsID == "" || blobID == "" {
			http.Error(w, "workspace id and blob id required", http.StatusBadRequest)
			return
		}
		if !callerCanAccessWorkspace(ctx, wsID) {
			http.Error(w, "no access to workspace", http.StatusForbidden)
			return
		}
		bc, err := cfg.GetBlob(ctx, blobID)
		if err != nil || bc.WorkspaceID != wsID {
			http.Error(w, "blob not found", http.StatusNotFound)
			return
		}
		ct := bc.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		if bc.Filename != "" {
			w.Header().Set("Content-Disposition", "attachment; filename=\""+bc.Filename+"\"")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bc.Content)
	}
}

// callerCanAccessWorkspace reports whether the caller may reach the workspace,
// reusing project_list {workspace_id} — whose authz is exactly "global admin or
// workspace member" (canListWorkspaceProjects). A workspace with zero projects
// still authorizes a member. Any dispatch error (forbidden / unconfigured) →
// false, so the blob routes fail closed.
func callerCanAccessWorkspace(ctx context.Context, wsID string) bool {
	req, _ := json.Marshal(verb.ProjectListRequest{WorkspaceID: wsID})
	_, err := verb.Dispatch(ctx, "project_list", req)
	return err == nil
}
