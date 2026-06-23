// project_documents.go — the project-page Documents panel + its upload endpoint
// (epic:phases-task-outputs). Project documents are the substrate tasks read as
// inputs (B1) and write as outputs (B2); this surfaces them on the project page
// like the stories/tasks panels — a filterable list with inline preview of text
// documents — plus a session-gated upload that mirrors the workspace corpus
// uploader. All reads go through the document verbs; the upload reuses the
// scope-generic ingest path with the project pinned. No new MCP verb.
package server

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/extract"
	"github.com/bobmcallan/satellites/internal/kvtag"
	"github.com/bobmcallan/satellites/internal/verb"
)

// docRow is one project document in the Documents panel. TypeTag/PhaseTag are the
// A1 KV classification (kvtag), rendered as chips; Expandable gates the inline
// body preview (text documents only) and BodyHTML carries the rendered body for
// the visible, expandable rows.
type docRow struct {
	ID       string
	Name     string
	TypeTag  string
	PhaseTag string
	Tags     []string
	// OtherTags is Tags with the type:/phase: KV tags removed, so the panel
	// renders the phase chip plus any remaining tags ONCE (no duplicate chips).
	OtherTags  []string
	UpdatedAt  time.Time
	Expandable bool
	BodyHTML   template.HTML
}

// otherTags returns tags with the type:/phase: KV pairs removed — the phase is
// rendered as its own chip and the type is constant (type:document), so neither
// should re-appear in the generic tag list.
func otherTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if strings.HasPrefix(t, "type:") || strings.HasPrefix(t, "phase:") {
			continue
		}
		out = append(out, t)
	}
	return out
}

// dispatchDocRows lists documents matching a filter as lightweight rows — enough
// to filter + count without fetching each body. The inline-preview payload
// (BodyHTML) is added later for the visible rows only. Callers constrain the
// filter to the KV `type:document` classification (not merely the row type), so
// principle / reference / contract / skill rows — document-rows carrying other KV
// tags — never appear: a "document" here is specifically a `type:document` row.
func dispatchDocRows(ctx context.Context, listReq verb.DocumentListRequest) ([]docRow, error) {
	body, _ := json.Marshal(listReq)
	raw, err := verb.Dispatch(ctx, "document_list", body)
	if err != nil {
		return nil, err
	}
	var resp verb.DocumentListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]docRow, 0, len(resp.Items))
	for _, d := range resp.Items {
		out = append(out, docRow{
			ID:        d.ID,
			Name:      d.Name,
			TypeTag:   kvtag.Value(d.Tags, "type"),
			PhaseTag:  kvtag.Value(d.Tags, "phase"),
			Tags:      d.Tags,
			OtherTags: otherTags(d.Tags),
			UpdatedAt: d.UpdatedAt,
		})
	}
	return out, nil
}

// projectDocListReq / workspaceDocListReq are the two `type:document`-constrained
// filters the panels list from — a project's attached documents, and a
// workspace's corpus documents.
func projectDocListReq(projectID string) verb.DocumentListRequest {
	return verb.DocumentListRequest{Type: "document", ProjectID: projectID, Tags: []string{"type:document"}, Limit: 200}
}

func workspaceDocListReq(workspaceID string) verb.DocumentListRequest {
	return verb.DocumentListRequest{Scope: "workspace", WorkspaceID: workspaceID, Type: "document", Tags: []string{"type:document"}, Limit: 200}
}

// matchDoc adapts the shared panel predicate (matchFields) to a document row:
// documents carry no status/priority/category, so those pass empty (no story
// `status:open` default is applied to documents). `type:`/`phase:`/free text all
// resolve against the row's id+name+tags haystack, the same way the stories and
// tasks panels treat their chip grammar.
func matchDoc(d docRow, q storyQuery) bool {
	return matchFields(d.ID, d.Name, "", "", "", d.Tags, q)
}

// filterDocs returns the subset of rows matching q, order preserved.
func filterDocs(rows []docRow, q storyQuery) []docRow {
	out := make([]docRow, 0, len(rows))
	for _, r := range rows {
		if matchDoc(r, q) {
			out = append(out, r)
		}
	}
	return out
}

// binaryDocExts / binaryDocTypes name the documents that should NOT expand
// inline — their inline preview is not meaningful text. Everything else (the
// markdown/text/mdx family, agent-authored `type:document`/`diagram` docs, and
// uploaded extracted-text wrappers) expands.
var binaryDocExts = []string{".pdf", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".zip", ".bin", ".exe"}
var binaryDocTypes = map[string]bool{"pdf": true, "image": true, "binary": true, "blob": true, "octet-stream": true}

// docExpandable reports whether a document row's body should be previewable
// inline. Known text types (markdown / mdx / text, and untyped/agent docs whose
// bodies are markdown) expand; clearly-binary rows (by KV type or filename
// extension) do not — they show metadata only (this story AC3).
func docExpandable(name, kvType string) bool {
	if binaryDocTypes[strings.ToLower(strings.TrimSpace(kvType))] {
		return false
	}
	lower := strings.ToLower(name)
	for _, ext := range binaryDocExts {
		if strings.HasSuffix(lower, ext) {
			return false
		}
	}
	return true
}

// gatherDocPanel resolves the documents-panel slice for a project given the
// request query (docs_q). It lists every document (the project-wide total),
// applies the chip filter (empty docs_q = show all — documents have no
// open/terminal lifecycle), then renders the inline body for the visible,
// expandable rows. Returns (rows, filteredCount, total). Read-only.
func gatherDocPanel(ctx context.Context, projectID string, q url.Values) ([]docRow, int, int, error) {
	return gatherDocPanelFrom(ctx, projectDocListReq(projectID), q)
}

// gatherWorkspaceDocPanel is the workspace-corpus peer of gatherDocPanel — the
// same filterable/expandable panel, scoped to a workspace's `type:document` rows.
func gatherWorkspaceDocPanel(ctx context.Context, workspaceID string, q url.Values) ([]docRow, int, int, error) {
	return gatherDocPanelFrom(ctx, workspaceDocListReq(workspaceID), q)
}

// gatherDocPanelFrom is the scope-agnostic core shared by the project and
// workspace documents panels: list the `type:document` rows the filter selects,
// apply the chip filter (docs_q), then render the inline body for the visible,
// expandable rows. Returns (rows, filteredCount, total). Read-only.
func gatherDocPanelFrom(ctx context.Context, listReq verb.DocumentListRequest, q url.Values) ([]docRow, int, int, error) {
	all, err := dispatchDocRows(ctx, listReq)
	if err != nil {
		return nil, 0, 0, err
	}
	total := len(all)
	dq := parseStoryQuery(strings.TrimSpace(q.Get("docs_q")))
	filtered := filterDocs(all, dq)
	filteredCount := len(filtered)
	for i := range filtered {
		filtered[i].Expandable = docExpandable(filtered[i].Name, filtered[i].TypeTag)
		if !filtered[i].Expandable {
			continue
		}
		body, gErr := dispatchStoryBody(ctx, filtered[i].ID)
		if gErr != nil {
			arbor.WarnCtx(ctx, "project_documents: body", "id", filtered[i].ID, "err", gErr)
			continue
		}
		if strings.TrimSpace(body) != "" {
			filtered[i].BodyHTML = renderMarkdown(body)
		}
	}
	return filtered, filteredCount, total, nil
}

// projectDocumentUploadHandler serves POST /projects/{id}/documents — the
// project page's add-document control (drag-drop / browse). Session-gated like
// the other portal pages (NOT the Bearer-gated POST /projects/{id}/blobs), so a
// browser fetch from the project detail page is authorised. It reuses the SAME
// scope-generic ingestion path as the workspace uploader (cfg.StoreBlob →
// ingest.StoreBlobAndExtract) with the PROJECT pinned, so the blob + extracted
// document are project-scoped. Access is global-admin OR project member; a
// non-member is refused, fail-closed.
func projectDocumentUploadHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Error(w, "not signed in", http.StatusUnauthorized)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)

		projectID := r.PathValue("id")
		if projectID == "" {
			http.Error(w, "project id required", http.StatusBadRequest)
			return
		}
		workspaceID, ok := callerProjectWorkspace(ctx, projectID)
		if !ok {
			http.Error(w, "you are not a member of this project", http.StatusForbidden)
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
			WorkspaceID: workspaceID, // the blob store requires it; an empty one 500s (this fix)
			ProjectID:   projectID,   // project-scoped document (vs the workspace corpus)
			Filename:    header.Filename,
			ContentType: contentType,
			CreatedBy:   userID,
			Content:     content,
		})
		if err != nil {
			arbor.ErrorCtx(ctx, "project document upload: store", "project_id", projectID, "err", err)
			http.Error(w, "could not store the document", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(ref)
	}
}

// callerProjectWorkspace resolves the project's workspace id AND enforces access in
// one project_get: the verb authorises project-scope read membership (a clean read
// means the caller is a member or global admin), and its response carries the
// project's workspace_id. The blob store REQUIRES that workspace_id — an upload that
// omits it is rejected and the handler 500s (the project-upload bug this fixes). The
// project_get response embeds project.Project, so workspace_id is a top-level field.
// Fail-closed: any error (forbidden, not-found, malformed) returns ("", false).
func callerProjectWorkspace(ctx context.Context, projectID string) (string, bool) {
	req, _ := json.Marshal(verb.ProjectGetRequest{ID: projectID})
	raw, err := verb.Dispatch(ctx, "project_get", req)
	if err != nil {
		return "", false
	}
	var resp struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || resp.WorkspaceID == "" {
		return "", false
	}
	return resp.WorkspaceID, true
}
