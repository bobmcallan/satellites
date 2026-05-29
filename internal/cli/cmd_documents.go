// `satellites document upload` — walk the committed substrate source
// tree under config/ and dispatch each markdown file as a
// document_upsert call. Sibling commands `satellites skill upload`
// and `satellites principle upload` (in cmd_skill.go / cmd_principle.go)
// reuse the same plan + dispatch path, each filtering to its own kind
// directory.
//
// The same `document` parent also carries `list` and `get` — thin
// shells over the document_list / document_get MCP verbs with
// type:"document" filtering (see cmd_substrate_noun.go). No new MCP
// verbs; all three nouns share the substrate's existing document_*
// surface.
//
// Source layout (the only shape these commands understand):
//
//   config/<workspace_id>/<kind>/<name>.md              → workspace scope
//   config/<workspace_id>/<project_id>/<kind>/<name>.md → project scope
//
// where <kind> is one of {documents, skills, principles}. The path
// carries the identity: scope, workspace_id, and project_id are derived
// from the directory segments, NOT from frontmatter. Frontmatter still
// supplies the optional name override, type override, and tags.
//
// config/documents/ is reserved for system seeds — it is embedded in
// the server binary and reconciled at boot (see config/documents/embed.go),
// never CLI-uploaded. The walker skips that subtree.
//
// Role-gating happens server-side in verb.authorizeWrite: the caller
// must be a member of the target workspace to write at workspace or
// project scope; system writes are flatly refused.
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

// configRoot is the committed substrate source tree, relative to CWD.
// Hard-coded for the same reason `satellites seed push` hard-codes its
// root — the convention IS the contract.
const configRoot = "config"

// systemSeedDir is the one child of config/ that is NOT a CLI upload
// target: it holds the system seeds embedded in the server binary and
// reconciled at boot. The walker skips it.
const systemSeedDir = "documents"

// kindDirs are the per-scope source subdirectories. Each maps to the
// default upsert type applied when a file omits `type:` in frontmatter.
var kindDirs = map[string]string{
	"documents":  "document",
	"skills":     "skill",
	"principles": "document",
}

// documentTarget describes one .md file scheduled for a dispatch.
type documentTarget struct {
	Path        string // path relative to CWD (printable)
	Scope       string // "workspace" | "project"
	WorkspaceID string
	ProjectID   string   // empty for workspace scope
	Name        string   // resolved name (frontmatter override OR filename stem)
	Type        string   // "document" | "skill" — frontmatter value, defaulted per-kind
	Tags        []string // optional, from frontmatter
	Body        string
}

func init() {
	var (
		configArg string
		userArg   string
	)
	docs := &cobra.Command{
		Use:   "document",
		Short: "Substrate document operations (list / get / upload)",
	}
	docs.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	docs.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")

	docs.AddCommand(newSubstrateListCmd(substrateNounConfig{
		Use:        "list",
		Short:      "List documents (document_list type:\"document\")",
		FilterType: "document",
		ConfigArg:  &configArg,
		UserArg:    &userArg,
	}))
	docs.AddCommand(newSubstrateGetCmd(substrateNounConfig{
		Use:        "get",
		Short:      "Print a document body (document_get name=<name>)",
		FilterType: "document",
		ConfigArg:  &configArg,
		UserArg:    &userArg,
	}))
	docs.AddCommand(newUploadCmd(uploadConfig{
		Kind:      "documents",
		ConfigArg: &configArg,
		UserArg:   &userArg,
	}))
	register(docs)
}

// uploadConfig describes one noun's upload command shape.
type uploadConfig struct {
	Kind      string  // "documents" | "skills" | "principles" — the kind dir this command uploads
	ConfigArg *string // shared --config flag
	UserArg   *string // shared --user flag
}

// newUploadCmd builds an `upload` cobra command bound to a kind dir.
// Used by `document upload`, `skill upload`, and `principle upload` to
// keep the three commands byte-identical except for the kind directory
// they walk under config/.
func newUploadCmd(cfg uploadConfig) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "upload",
		Short: fmt.Sprintf("Walk config/**/%s and upsert each file via document_upsert", cfg.Kind),
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := planUpload(configRoot, cfg.Kind)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(targets) == 0 {
				fmt.Fprintf(out, "no %s found under config/**/%s/ — nothing to upload\n", cfg.Kind, cfg.Kind)
				return nil
			}
			for _, t := range targets {
				label := uploadLabel(t)
				if dryRun {
					fmt.Fprintf(out, "[dry-run] %s → (%s, %s, %s)\n", t.Path, t.Type, t.Scope, t.Name)
					continue
				}
				req, marshalErr := marshalUpsertRequest(t)
				if marshalErr != nil {
					return fmt.Errorf("%s: %w", t.Path, marshalErr)
				}
				resp, err := dispatchVerb(context.Background(), "document_upsert", req, *cfg.ConfigArg, *cfg.UserArg)
				if err != nil {
					return fmt.Errorf("%s: %w", t.Path, err)
				}
				summary := summariseUploadResp(resp)
				fmt.Fprintf(out, "%s → %s (%s)\n", t.Path, label, summary)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the planned dispatches (scope, type, name) without calling the verbs")
	return cmd
}

// planUpload walks rootDir recursively and returns the ordered list of
// upserts for the given kind directory. Files whose path does not
// resolve to the requested kind are skipped; the config/documents
// system-seed subtree is skipped entirely. Files are ordered:
// workspace-scope first, then project-scope, then by path within each
// group — so a workspace-scope document lands before any project doc
// that might inherit it.
func planUpload(rootDir, kind string) ([]documentTarget, error) {
	info, err := os.Stat(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("upload: stat %s: %w", rootDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("upload: %s is not a directory", rootDir)
	}

	var workspaces, projects []documentTarget
	err = filepath.WalkDir(rootDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip the system-seed subtree — those are embedded and
			// boot-reconciled, never CLI-uploaded.
			if p == filepath.Join(rootDir, systemSeedDir) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		target, want, classifyErr := classifyDocumentFile(rootDir, p, kind)
		if classifyErr != nil {
			return classifyErr
		}
		if !want {
			return nil
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

// classifyDocumentFile derives a file's identity from its path under
// rootDir and parses its frontmatter. It returns want=false (no error)
// when the file's kind directory does not match the requested kind, so
// each command picks up only its own kind. Scope, workspace_id, and
// project_id come from the path segments — never from frontmatter; the
// path is the single source of identity. The default upsert type is
// the kind's mapped type, overridable by `type:` in frontmatter.
//
// Recognised layouts (segments below rootDir):
//
//	<workspace_id>/<kind>/<name>.md              → workspace scope
//	<workspace_id>/<project_id>/<kind>/<name>.md → project scope
func classifyDocumentFile(rootDir, filePath, wantKind string) (documentTarget, bool, error) {
	rel, err := filepath.Rel(rootDir, filePath)
	if err != nil {
		return documentTarget{}, false, fmt.Errorf("%s: relativise under %s: %w", filePath, rootDir, err)
	}
	segs := strings.Split(filepath.ToSlash(rel), "/")

	var scope, workspaceID, projectID, kindDir, filename string
	switch len(segs) {
	case 3: // <wksp>/<kind>/<file>
		scope, workspaceID, kindDir, filename = "workspace", segs[0], segs[1], segs[2]
	case 4: // <wksp>/<proj>/<kind>/<file>
		scope, workspaceID, projectID, kindDir, filename = "project", segs[0], segs[1], segs[2], segs[3]
	default:
		return documentTarget{}, false, fmt.Errorf("%s: unexpected source layout — expected config/<wksp>[/<proj>]/<kind>/<name>.md", filePath)
	}

	defaultType, known := kindDirs[kindDir]
	if !known {
		return documentTarget{}, false, fmt.Errorf("%s: unknown kind directory %q (allowed: documents, skills, principles)", filePath, kindDir)
	}
	if kindDir != wantKind {
		return documentTarget{}, false, nil
	}

	body, fm, err := readFileWithFrontmatter(filePath)
	if err != nil {
		return documentTarget{}, false, err
	}
	docType := strings.TrimSpace(fm.Type)
	if docType == "" {
		docType = defaultType
	}
	return documentTarget{
		Path:        filePath,
		Scope:       scope,
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Name:        resolveName(filename, fm.Name),
		Type:        docType,
		Tags:        fm.Tags,
		Body:        body,
	}, true, nil
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
	docType := t.Type
	if docType == "" {
		docType = "document"
	}
	payload := map[string]any{
		"type":         docType,
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
