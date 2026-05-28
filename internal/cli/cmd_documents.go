// `satellites documents upload` — walk .satellites/documents/ from the
// current working directory and dispatch each markdown file as a
// document_upsert call.
//
// Layout (the only shape this command understands):
//
//   .satellites/documents/<name>.md
//
// Identity (scope, workspace_id, project_id, name, tags) lives in each
// file's YAML frontmatter. The classifier does not derive scope from
// directory layout — missing `scope:` is a hard error. Scope drives
// the required-id set: `workspace` requires `workspace_id`; `project`
// requires both `workspace_id` and `project_id`.
//
// Idempotency is delegated to the substrate: the same body bytes
// produce zero new document_versions rows on a re-push. Tags merge via
// the document_upsert tag path (see internal/verb/document.go) — a
// re-push with equal tags is a no-op on the documents row too.
//
// --dry-run prints the planned dispatches without calling the verb.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/spf13/cobra"
)

// documentsRoot is the conventional location, relative to CWD. Hard-
// coded for the same reason `satellites seed push` hard-codes
// .satellites/seeds — the convention IS the contract.
const documentsRoot = ".satellites/documents"

// documentTarget describes one .md file scheduled for a dispatch.
type documentTarget struct {
	Path        string // path relative to CWD (printable)
	Scope       string // "workspace" | "project"
	WorkspaceID string
	ProjectID   string   // empty for workspace scope
	Name        string   // resolved name (frontmatter override OR filename stem)
	Tags        []string // optional, from frontmatter
	Body        string
}

func init() {
	var (
		configArg string
		userArg   string
		dryRun    bool
	)
	docs := &cobra.Command{
		Use:   "documents",
		Short: "File-based document substrate operations",
	}
	docs.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	docs.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")

	upload := &cobra.Command{
		Use:   "upload",
		Short: "Walk .satellites/documents/ and upsert each file via document_upsert",
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := planDocumentsUpload(documentsRoot)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(targets) == 0 {
				fmt.Fprintln(out, "no documents found under .satellites/documents/ — nothing to upload")
				return nil
			}
			for _, t := range targets {
				label := uploadLabel(t)
				if dryRun {
					fmt.Fprintf(out, "[dry-run] %s → %s\n", t.Path, label)
					continue
				}
				req, marshalErr := marshalUpsertRequest(t)
				if marshalErr != nil {
					return fmt.Errorf("%s: %w", t.Path, marshalErr)
				}
				resp, err := dispatchVerb(context.Background(), "document_upsert", req, configArg, userArg)
				if err != nil {
					return fmt.Errorf("%s: %w", t.Path, err)
				}
				summary := summariseUploadResp(resp)
				fmt.Fprintf(out, "%s → %s (%s)\n", t.Path, label, summary)
			}
			return nil
		},
	}
	upload.Flags().BoolVar(&dryRun, "dry-run", false, "Print the planned dispatches without calling the verbs")
	docs.AddCommand(upload)
	register(docs)
}

// planDocumentsUpload walks rootDir (flat — no nested subdirectories)
// and returns the ordered list of upserts. Files are ordered:
// workspace-scope first, then project-scope, then by path within each
// group — so a workspace-scope document lands before any project doc
// that might inherit it.
func planDocumentsUpload(rootDir string) ([]documentTarget, error) {
	info, err := os.Stat(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("documents upload: stat %s: %w", rootDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("documents upload: %s is not a directory", rootDir)
	}

	var workspaces, projects []documentTarget
	err = filepath.WalkDir(rootDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip nested directories — the flat layout is the contract.
			// Only the rootDir itself is walked.
			if p == rootDir {
				return nil
			}
			return fs.SkipDir
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		target, classifyErr := classifyDocumentFile(p, d.Name())
		if classifyErr != nil {
			return classifyErr
		}
		switch target.Scope {
		case "workspace":
			workspaces = append(workspaces, target)
		case "project":
			projects = append(projects, target)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].Path < workspaces[j].Path })
	sort.Slice(projects, func(i, j int) bool { return projects[i].Path < projects[j].Path })
	return append(workspaces, projects...), nil
}

// classifyDocumentFile reads filePath, parses frontmatter, and returns
// a populated target. Scope is required — files without `scope:` in
// frontmatter are rejected so a misauthored file fails loudly rather
// than uploading to the wrong destination. workspace_id is required
// for both `workspace` and `project` scopes; project_id additionally
// for `project`.
func classifyDocumentFile(filePath, filename string) (documentTarget, error) {
	body, fm, err := readFileWithFrontmatter(filePath)
	if err != nil {
		return documentTarget{}, err
	}
	scope := strings.TrimSpace(fm.Scope)
	if scope == "" {
		return documentTarget{}, fmt.Errorf("%s: frontmatter must declare scope (workspace or project)", filePath)
	}
	t := documentTarget{
		Path:        filePath,
		Scope:       scope,
		WorkspaceID: fm.WorkspaceID,
		ProjectID:   fm.ProjectID,
		Name:        resolveName(filename, fm.Name),
		Tags:        fm.Tags,
		Body:        body,
	}
	switch scope {
	case "workspace":
		if t.WorkspaceID == "" {
			return documentTarget{}, fmt.Errorf("%s: scope=workspace requires workspace_id in frontmatter", filePath)
		}
	case "project":
		if t.WorkspaceID == "" || t.ProjectID == "" {
			return documentTarget{}, fmt.Errorf("%s: scope=project requires both workspace_id and project_id in frontmatter", filePath)
		}
	default:
		return documentTarget{}, fmt.Errorf("%s: unsupported scope %q (allowed: workspace, project)", filePath, scope)
	}
	return t, nil
}

// readFileWithFrontmatter loads the file at p and splits frontmatter
// from body. Returns the body string and the parsed frontmatter.
func readFileWithFrontmatter(p string) (string, frontmatter.Frontmatter, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", frontmatter.Frontmatter{}, fmt.Errorf("read %s: %w", p, err)
	}
	fm, body, err := frontmatter.Parse(raw)
	if err != nil {
		return "", frontmatter.Frontmatter{}, fmt.Errorf("parse %s: %w", p, err)
	}
	return string(body), fm, nil
}

// resolveName returns the frontmatter-declared name when present;
// otherwise the filename stem with the `.md` extension stripped.
func resolveName(filename, override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	return strings.TrimSuffix(filename, ".md")
}

// uploadLabel renders the dispatch target as `<scope>/<workspace>[/<project>]/<name>`
// for the per-file progress line.
func uploadLabel(t documentTarget) string {
	switch t.Scope {
	case "workspace":
		return fmt.Sprintf("workspace/%s/%s", t.WorkspaceID, t.Name)
	case "project":
		return fmt.Sprintf("project/%s/%s/%s", t.WorkspaceID, t.ProjectID, t.Name)
	default:
		return t.Name
	}
}

// marshalUpsertRequest builds the JSON document_upsert payload for a
// single file. Tags are passed through as-is; the request omits the
// tag pointer when no tags are declared (frontmatter absent), matching
// the document_upsert "leave alone" semantics for that field.
func marshalUpsertRequest(t documentTarget) (json.RawMessage, error) {
	payload := map[string]any{
		"type":         "document",
		"scope":        t.Scope,
		"workspace_id": t.WorkspaceID,
		"name":         t.Name,
		"body":         t.Body,
	}
	if t.ProjectID != "" {
		payload["project_id"] = t.ProjectID
	}
	if t.Tags != nil {
		payload["tags"] = t.Tags
	}
	return json.Marshal(payload)
}

// summariseUploadResp reads the document_upsert response to report
// "applied" (a new version) or "no change". We inspect latest_version
// + version.created_at on the response; the substrate already
// guarantees zero new version rows for byte-equal bodies, but the
// response shape always returns the latest row, so the heuristic is:
// when latest_version > 1 we cannot tell at the verb level whether
// this call appended; report "applied" by default. A future
// "applied" flag on the verb would tighten this.
func summariseUploadResp(resp json.RawMessage) string {
	var parsed struct {
		Version struct {
			Version int `json:"version"`
		} `json:"version"`
		Document struct {
			LatestVersion int `json:"latest_version"`
		} `json:"document"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return "ok"
	}
	if parsed.Version.Version == parsed.Document.LatestVersion {
		return fmt.Sprintf("version=%d", parsed.Version.Version)
	}
	return "ok"
}
