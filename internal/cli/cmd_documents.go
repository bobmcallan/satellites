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
	"io"
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

// validSkillKinds is the dispatch-contract `kind` enum every type:skill
// source must declare (sty_3359cb48), so the dynamic index can classify a
// skill from frontmatter alone.
var validSkillKinds = map[string]bool{
	"workflow":   true,
	"function":   true,
	"gate":       true,
	"capability": true,
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
	var (
		dryRun     bool
		skipReview bool
	)
	cmd := &cobra.Command{
		Use:   "upload",
		Short: fmt.Sprintf("Walk config/**/%s and upsert each file via document_upsert", cfg.Kind),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// Validate the whole source tree against the deterministic
			// inclusion rule (document:project/project-substrate-inclusion)
			// before any dispatch. Every noun's upload runs this — a mis-typed
			// file anywhere refuses the push, naming file + rule. Pure read,
			// so it also runs under --dry-run (sty_50ecb56f).
			violations, err := validateUpload(configRoot, filepath.Join(".claude", "skills"))
			if err != nil {
				return err
			}
			if len(violations) > 0 {
				fmt.Fprintf(out, "validation failed — %d violation(s), nothing uploaded:\n", len(violations))
				for _, v := range violations {
					fmt.Fprintf(out, "  ✗ %s\n", v.String())
				}
				return fmt.Errorf("upload refused: %d validation violation(s)", len(violations))
			}
			return uploadKind(ctx, out, cfg.Kind, *cfg.ConfigArg, *cfg.UserArg, dryRun, skipReview)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the planned dispatches (scope, type, name) without calling the verbs")
	cmd.Flags().BoolVar(&skipReview, "skip-review", false, "Skip the strict content review (drift-prone reference check) — use only after running the per-type review skill")
	return cmd
}

// uploadKind plans and dispatches the document_upsert calls for one kind
// directory under config/. It does NOT validate — callers run
// validateUpload first (newUploadCmd does). Single source for the
// plan+dispatch loop shared by the per-noun `upload` commands. `satellites
// deploy` is pull-only and no longer calls this (sty_2fa6f087 follow-up).
func uploadKind(ctx context.Context, out io.Writer, kind, configArg, userArg string, dryRun, skipReview bool) error {
	targets, err := planUpload(configRoot, kind)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Fprintf(out, "no %s found under config/**/%s/ — nothing to upload\n", kind, kind)
		return nil
	}
	reviewSkill := reviewSkillForKind(kind)
	for _, t := range targets {
		label := uploadLabel(t)
		if dryRun {
			fmt.Fprintf(out, "[dry-run] %s → (%s, %s, %s)\n", t.Path, t.Type, t.Scope, t.Name)
			continue
		}
		// Strict content review before dispatch (sty_f302bd8b): block a
		// durable artifact that hard-codes drift-prone references. The
		// per-type review skill carries the maintainability critique the
		// local agent runs; --skip-review overrides after that review.
		if !skipReview {
			if findings := reviewContent(t.Body); len(findings) > 0 {
				fmt.Fprintf(out, "content-review blocked %s — %d drift-prone reference(s); run skill %q for the maintainability critique, or pass --skip-review:\n",
					t.Path, len(findings), reviewSkill)
				for _, f := range findings {
					fmt.Fprintf(out, "  ✗ %s\n", f.String())
				}
				return fmt.Errorf("%s: content review blocked %d drift-prone reference(s) (override with --skip-review)", t.Path, len(findings))
			}
		}
		req, marshalErr := marshalUpsertRequest(t)
		if marshalErr != nil {
			return fmt.Errorf("%s: %w", t.Path, marshalErr)
		}
		resp, err := dispatchVerb(ctx, "document_upsert", req, configArg, userArg)
		if err != nil {
			return fmt.Errorf("%s: %w", t.Path, err)
		}
		fmt.Fprintf(out, "%s → %s (%s)\n", t.Path, label, summariseUploadResp(resp))
	}
	return nil
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

// violation is one inclusion-rule breach the validator found: the
// offending file (or skill name) and the rule it broke. The String form
// is what `upload` prints before refusing.
type violation struct {
	Path string // source file path, or .claude/skills/<name> for drift
	Rule string // short rule id (type-mismatch, skill-frontmatter, …)
	Msg  string // human-readable detail
}

func (v violation) String() string {
	return fmt.Sprintf("%s [%s] %s", v.Path, v.Rule, v.Msg)
}

// validateUpload checks every source under rootDir against the deterministic
// inclusion rule (document:project/project-substrate-inclusion) and flags
// drift against the materialised .claude/skills tree. It is a pure read — no
// dispatch, no writes — so a re-run on the same inputs yields the same
// verdict, which is what lets `--dry-run` use it as a standalone check
// (sty_50ecb56f). An empty result means the tree is clean.
//
// Checks, all mechanical (no judgement call):
//   - path layout resolves to config/<wksp>[/<proj>]/<kind>/<name>.md;
//   - <kind> is a known kind directory;
//   - frontmatter `type:`, when set, matches the kind dir's mapped type
//     (a skills/ file may not declare type:document, etc.);
//   - frontmatter scope/workspace_id/project_id, when set, match the path;
//   - a skills/ file carries the required `name` + `description` frontmatter;
//   - drift: a stamped (sync-materialised, hence project-owned) skill in
//     .claude/skills/ with no config/.../skills/ source. Unstamped local
//     skills are operator-owned and ignored.
func validateUpload(rootDir, skillsRoot string) ([]violation, error) {
	info, err := os.Stat(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("validate: stat %s: %w", rootDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("validate: %s is not a directory", rootDir)
	}

	var vs []violation
	configSkillNames := map[string]bool{}

	walkErr := filepath.WalkDir(rootDir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			if p == filepath.Join(rootDir, systemSeedDir) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		rel, relErr := filepath.Rel(rootDir, p)
		if relErr != nil {
			return relErr
		}
		segs := strings.Split(filepath.ToSlash(rel), "/")
		var scope, wsID, pjID, kindDir, filename string
		switch len(segs) {
		case 3:
			scope, wsID, kindDir, filename = "workspace", segs[0], segs[1], segs[2]
		case 4:
			scope, wsID, pjID, kindDir, filename = "project", segs[0], segs[1], segs[2], segs[3]
		default:
			vs = append(vs, violation{p, "layout", "unexpected source layout — expected config/<wksp>[/<proj>]/<kind>/<name>.md"})
			return nil
		}

		defaultType, known := kindDirs[kindDir]
		if !known {
			vs = append(vs, violation{p, "kind-dir", fmt.Sprintf("unknown kind directory %q (allowed: documents, skills, principles)", kindDir)})
			return nil
		}

		_, _, fm, ferr := readFileWithFrontmatter(p)
		if ferr != nil {
			vs = append(vs, violation{p, "frontmatter", ferr.Error()})
			return nil
		}

		if t := strings.TrimSpace(fm.Type); t != "" && t != defaultType {
			vs = append(vs, violation{p, "type-mismatch", fmt.Sprintf("frontmatter type:%q under %s/ — expected %q", t, kindDir, defaultType)})
		}
		if s := strings.TrimSpace(fm.Scope); s != "" && s != scope {
			vs = append(vs, violation{p, "scope-mismatch", fmt.Sprintf("frontmatter scope:%q != path scope %q", s, scope)})
		}
		if w := strings.TrimSpace(fm.WorkspaceID); w != "" && w != wsID {
			vs = append(vs, violation{p, "workspace-mismatch", fmt.Sprintf("frontmatter workspace_id:%q != path %q", w, wsID)})
		}
		if pj := strings.TrimSpace(fm.ProjectID); pj != "" && pj != pjID {
			vs = append(vs, violation{p, "project-mismatch", fmt.Sprintf("frontmatter project_id:%q != path %q", pj, pjID)})
		}

		if kindDir == "skills" {
			if strings.TrimSpace(fm.Name) == "" {
				vs = append(vs, violation{p, "skill-frontmatter", "skill missing required frontmatter: name"})
			}
			if strings.TrimSpace(fm.Description) == "" {
				vs = append(vs, violation{p, "skill-frontmatter", "skill missing required frontmatter: description"})
			}
			// Dispatch contract (sty_3359cb48): the dynamic index dispatches
			// off frontmatter alone, so every skill declares a kind; a
			// workflow additionally declares the story types it binds.
			switch k := strings.TrimSpace(fm.Kind); {
			case k == "":
				vs = append(vs, violation{p, "skill-dispatch", "skill missing required frontmatter: kind (workflow|function|gate|capability)"})
			case !validSkillKinds[k]:
				vs = append(vs, violation{p, "skill-dispatch", fmt.Sprintf("skill kind:%q is not one of workflow|function|gate|capability", k)})
			case k == "workflow" && len(fm.AppliesTo) == 0:
				vs = append(vs, violation{p, "skill-dispatch", "workflow skill missing required frontmatter: applies_to"})
			}
			configSkillNames[resolveName(filename, fm.Name)] = true
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	// Drift: a project-owned (stamped) skill on disk with no config source.
	stamped, serr := readStampedLocalSkills(skillsRoot)
	if serr != nil {
		return nil, fmt.Errorf("validate: scan %s: %w", skillsRoot, serr)
	}
	for _, l := range stamped {
		if !configSkillNames[l.Name] {
			vs = append(vs, violation{
				filepath.Join(skillsRoot, l.Name),
				"orphan-skill",
				"project-owned (stamped) skill in .claude/skills/ with no config/.../skills/ source",
			})
		}
	}
	return vs, nil
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

	raw, body, fm, err := readFileWithFrontmatter(filePath)
	if err != nil {
		return documentTarget{}, false, err
	}
	docType := strings.TrimSpace(fm.Type)
	if docType == "" {
		docType = defaultType
	}
	// Skills preserve their frontmatter in the stored body: the substrate
	// row must be a complete, registerable SKILL.md (name/description) so a
	// client can materialise .claude/skills/<name>/SKILL.md from it. This
	// mirrors the server's system-seed path (cmd/satellites-server/main.go:
	// `storedBody = raw` for TypeSkill). Documents and principles strip
	// frontmatter — substrate keys are not part of their rendered body
	// (sty_4b517016). Compared against the "skill" string, not the
	// document.TypeSkill constant, to honour the CLI layering guard (no
	// internal/document import from the cli package).
	storedBody := body
	if docType == "skill" {
		storedBody = raw
	}
	return documentTarget{
		Path:        filePath,
		Scope:       scope,
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Name:        resolveName(filename, fm.Name),
		Type:        docType,
		Tags:        fm.Tags,
		Body:        storedBody,
	}, true, nil
}

// readFileWithFrontmatter loads the file at p and splits frontmatter
// from body. Returns the raw file (frontmatter intact), the
// frontmatter-stripped body, and the parsed frontmatter — the caller
// picks raw vs stripped per type (skills keep frontmatter, documents
// strip it).
func readFileWithFrontmatter(p string) (string, string, frontmatter.Frontmatter, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", "", frontmatter.Frontmatter{}, fmt.Errorf("read %s: %w", p, err)
	}
	fm, body, err := frontmatter.Parse(raw)
	if err != nil {
		return "", "", frontmatter.Frontmatter{}, fmt.Errorf("parse %s: %w", p, err)
	}
	return string(raw), string(body), fm, nil
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
