// `satellites document upload` — walk the repo-local substrate source
// tree under .satellites/ and dispatch each markdown file as a
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
//   .satellites/<kind>/<name>.md → project scope
//
// where <kind> is one of {documents, skills, principles}. The layout is
// flat and project-scoped: there is no workspace/project nesting. The
// owning project_id is read from .satellites/satellites.toml (the repo is
// bound to exactly one project), NOT from path segments or frontmatter.
// The server derives the workspace from that project. Frontmatter supplies
// the optional name override, type override, and tags. Workspace scope is
// not yet supported (sty_afc0769c).
//
// System seeds are a separate pipeline: they live under config/documents/,
// are embedded in the server binary, and are reconciled at boot (see
// config/documents/embed.go) — never CLI-uploaded. Likewise .satellites/seeds/
// (seed_md applied via `satellites seed push`) is unrelated to this path.
//
// Role-gating happens server-side in verb.authorizeWrite: the caller must
// be a member of the project's workspace to write at project scope; system
// writes are flatly refused.
//
// Idempotency is delegated to the substrate: the same body bytes produce
// zero new document_versions rows on a re-push. Tags merge via the
// document_upsert tag path (see internal/verb/document.go) — a re-push with
// equal tags is a no-op on the documents row too.
//
// --dry-run prints the planned dispatches without calling the verb.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/spf13/cobra"
)

// artifactReviewTimeout caps the per-artifact reviewer run (a read-and-judge
// claude -p, no build) that gates every skill / principle upsert.
const artifactReviewTimeout = 5 * time.Minute

// substrateRootDefault is the DEFAULT parent directory holding a kind's
// authoring source folder (documents/, principles/, skills/) — the original
// convention (.satellites/<kind>/). It is only the default: the location is
// CONFIGURED per-kind via the toml's [substrate_roots] and resolved by
// resolveSubstrateRoot. Configuration over code — no baked path (sty_f2aa0ab5).
const substrateRootDefault = ".satellites"

// resolveSubstrateRoot returns the parent directory holding `kind`'s authoring
// source folder, from the toml's [substrate_roots] (per-kind), defaulting to
// `.satellites`. A relative value resolves against the repo root (the dir
// holding .satellites/, NOT the process CWD); absolute is used verbatim — so a
// repo authoring under the top-level skills/ sets `skills = "."`. Mirrors
// resolveSkillsRoot (sty_f2aa0ab5).
func resolveSubstrateRoot(kind, configArg string) string {
	root := substrateRootDefault
	cfg, path, err := cliconfig.Load(configArg)
	if err == nil {
		if r := strings.TrimSpace(cfg.SubstrateRoots[kind]); r != "" {
			root = r
		}
	}
	if filepath.IsAbs(root) {
		return root
	}
	return filepath.Join(cliconfig.RepoRootFromConfigPath(path), root)
}

// nudgeStaleSubstrateDir warns (one line, non-fatal) when a `.satellites/<kind>/`
// still holds .md sources while the kind's RESOLVED authoring root is elsewhere
// (e.g. configured to the repo root) — those files are no longer read. Surfacing
// it stops a silent "nothing uploaded". When the kind resolves to .satellites
// (the default), .satellites/<kind>/ IS the source, so it stays silent.
func nudgeStaleSubstrateDir(out io.Writer, kind, resolvedRoot string) {
	legacy := filepath.Join(substrateRootDefault, kind)
	// If the resolved source folder IS .satellites/<kind>, nothing is stale.
	if abs, lerr := filepath.Abs(filepath.Join(resolvedRoot, kind)); lerr == nil {
		if labs, lerr2 := filepath.Abs(legacy); lerr2 == nil && abs == labs {
			return
		}
	}
	entries, err := os.ReadDir(legacy)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			fmt.Fprintf(out, "note: %s/ holds sources but the %s authoring root is configured elsewhere (%s/%s) — those files are not read. Move or re-point them.\n", legacy, kind, resolvedRoot, kind)
			return
		}
	}
}

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
	"workflow": true,
	"function": true,
	// gate is the legacy name for a reviewer (a claude -p judgment that enacts a
	// transition); reviewer is the reviewers-only name. Both are accepted while
	// the gate→reviewer rename lands (0c); new reviewers should declare reviewer.
	"gate":       true,
	"reviewer":   true,
	"capability": true,
	// task (epic:workspace-agents): a workspace agent's unit of work — a spec a
	// server-side run executes over the workspace corpus (workspace_task_run).
	"task": true,
}

// documentTarget describes one .md file scheduled for a dispatch. Every
// target is project-scoped; the owning project_id comes from the repo
// config, not the path.
type documentTarget struct {
	Path      string   // path relative to CWD (printable)
	ProjectID string   // resolved from .satellites/satellites.toml
	Name      string   // resolved name (frontmatter override OR filename stem)
	Type      string   // "document" | "skill" — frontmatter value, defaulted per-kind
	Tags      []string // optional, from frontmatter
	Headline  string   // optional caveman one-liner, from frontmatter
	Level     string   // optional declared visibility (system|project|global), from frontmatter
	Scope     string   // optional explicit scope, from frontmatter (level fallback)
	Body      string
	Raw       string   // the full file (frontmatter + body) — what a reviewer judges, since Body strips frontmatter for documents/principles
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
	docs.AddCommand(newDocumentIndexCmd(&configArg, &userArg))
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
// they walk under .satellites/.
func newUploadCmd(cfg uploadConfig) *cobra.Command {
	var (
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "upload",
		Short: fmt.Sprintf("Walk the top-level %s/ folder and upsert each file via document_upsert", cfg.Kind),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// project_id is the repo's identity (sty_afc0769c): every upload is
			// project-scoped and the id comes from .satellites/satellites.toml,
			// never the path. Resolve once and thread it through validate + plan.
			projectID, err := projectIDFromConfig(*cfg.ConfigArg)
			if err != nil {
				return err
			}

			// Validate the whole source tree against the deterministic
			// inclusion rule (document:project/project-substrate-inclusion)
			// before any dispatch. Every noun's upload runs this — a mis-typed
			// file anywhere refuses the push, naming file + rule. Pure read,
			// so it also runs under --dry-run (sty_50ecb56f).
			violations, err := validateUpload(
				func(k string) string { return resolveSubstrateRoot(k, *cfg.ConfigArg) },
				filepath.Join(".claude", "skills"), projectID)
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
			return uploadKind(ctx, out, cfg.Kind, *cfg.ConfigArg, *cfg.UserArg, projectID, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the planned dispatches (type, name) without calling the verbs")
	return cmd
}

// projectIDFromConfig resolves the repo's project_id from satellites.toml
// (explicit --config path, else the $SATELLITES_CONFIG / walk-up default).
// An absent config or an unset project_id is a hard error — there is no
// path-encoded fallback any more (sty_afc0769c).
func projectIDFromConfig(configArg string) (string, error) {
	cfg, _, err := cliconfig.Load(configArg)
	if err != nil && !errors.Is(err, cliconfig.ErrNotFound) {
		return "", fmt.Errorf("upload: load config: %w", err)
	}
	pid := strings.TrimSpace(cfg.ProjectID)
	if pid == "" {
		return "", fmt.Errorf("upload: no project_id in .satellites/satellites.toml — set it or run `satellites project match`")
	}
	return pid, nil
}

// uploadKind plans and dispatches the document_upsert calls for one kind
// directory under .satellites/. It does NOT validate — callers run
// validateUpload first (newUploadCmd does). Single source for the
// plan+dispatch loop shared by the per-noun `upload` commands. `satellites
// deploy` is pull-only and no longer calls this (sty_2fa6f087 follow-up).
func uploadKind(ctx context.Context, out io.Writer, kind, configArg, userArg, projectID string, dryRun bool) error {
	root := resolveSubstrateRoot(kind, configArg)
	targets, err := planUpload(root, kind, projectID)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Fprintf(out, "no %s found under %s/ — nothing to upload\n", kind, filepath.Join(root, kind))
		nudgeStaleSubstrateDir(out, kind, root)
		return nil
	}
	reviewSkill := reviewSkillForKind(kind)
	// A kind is reviewer-gated IFF its per-type reviewer (satellites-<kind>-review)
	// resolves AND declares kind:reviewer — config, not a hardcoded kind list
	// (the constitution: which gate runs lives in the substrate). Authoring a new
	// per-kind reviewer turns its gate on with NO code change here. Resolved once;
	// the deterministic pre-filter in the loop always runs regardless.
	reviewDisp := verb.ClaudeCLIGateDispatcher{
		Model:          reviewerModel(configArg),
		DefaultTimeout: artifactReviewTimeout,
		Fetch:          serverGateFetcher(configArg, userArg),
	}
	gateWithReviewer, _ := reviewDisp.IsReviewer(ctx, ".", reviewSkill)
	for _, t := range targets {
		label := uploadLabel(t)
		if dryRun {
			fmt.Fprintf(out, "[dry-run] %s → (%s, project, %s)\n", t.Path, t.Type, t.Name)
			continue
		}
		// 1) Deterministic pre-filter (sty_f302bd8b): block an artifact that
		// hard-codes drift-prone references (and, for a skill, an unversioned
		// repo-script dependency). Always runs — the content-review:allow-refs tag
		// is the only exemption; there is no --skip-review bypass under the
		// reviewers-only model.
		if !hasReviewExemptTag(t.Tags) {
			findings := reviewContent(t.Body)
			if kind == "skills" {
				findings = append(findings, reviewSkillSelfContained(t.Body)...)
			}
			if len(findings) > 0 {
				fmt.Fprintf(out, "content-review blocked %s — %d drift-prone reference(s):\n", t.Path, len(findings))
				for _, f := range findings {
					fmt.Fprintf(out, "  ✗ %s\n", f.String())
				}
				return fmt.Errorf("%s: content review blocked %d drift-prone reference(s)", t.Path, len(findings))
			}
		}
		// 2) A skill or principle is gated by its per-type REVIEWER (a claude -p
		// judgment): it judges the proposed artifact on stdin and THIS command
		// enacts the verdict — accept upserts, reject blocks with the reviewer's
		// notes so the author revises and re-runs (the upsert analogue of a v2
		// story edge the client enacts). The reviewer is independent (fresh
		// context); the author never self-judges, and there is no bypass.
		// Documents (today) keep only the deterministic pre-filter — no per-type
		// reviewer resolves for them, so gateWithReviewer is false.
		if gateWithReviewer {
			verdict, rErr := reviewDisp.ReviewContent(ctx, verb.ContentReviewInput{
				SkillName:    reviewSkill,
				Content:      t.Raw, // full file incl. frontmatter — Body strips it for documents/principles
				WorktreeRoot: ".",
			})
			if rErr != nil {
				return fmt.Errorf("%s: review (%s): %w", t.Path, reviewSkill, rErr)
			}
			if verdict.Decision != verb.GateDecisionAccept {
				fmt.Fprintf(out, "%s REJECTED %s:\n  %s\n", reviewSkill, t.Path, strings.TrimSpace(verdict.Notes))
				return fmt.Errorf("%s: %s rejected — revise per the notes and re-run", t.Path, reviewSkill)
			}
			fmt.Fprintf(out, "%s accepted %s\n", reviewSkill, t.Path)
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
		// Caveman headline (epic:always-context): for a document/principle with
		// no stored headline, generate one client-side via claude -p and patch
		// it in. Best-effort — never aborts the upload.
		fillHeadlineIfEmpty(ctx, out, resp, t, "", configArg, userArg)
	}
	return nil
}

// planUpload walks .satellites/<kind>/ and returns the ordered list of
// upserts for that kind, one per flat `<name>.md` file. A missing kind dir
// yields no targets (nothing to upload). Files nested below the kind dir
// are skipped here — validateUpload flags them. Targets are ordered by
// path for deterministic output.
func planUpload(rootDir, kind, projectID string) ([]documentTarget, error) {
	kindRoot := filepath.Join(rootDir, kind)
	info, err := os.Stat(kindRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("upload: stat %s: %w", kindRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("upload: %s is not a directory", kindRoot)
	}

	var targets []documentTarget
	err = filepath.WalkDir(kindRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(kindRoot, p)
		if relErr != nil {
			return relErr
		}
		// Flat layout only: a file nested below the kind dir is not a valid
		// source (validateUpload reports it); don't dispatch it.
		if strings.Contains(filepath.ToSlash(rel), "/") {
			return nil
		}
		targets = append(targets, classifyDocumentFile(p, kind, projectID))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Path < targets[j].Path })
	return targets, nil
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

// validateUpload checks every source under .satellites/{documents,skills,
// principles}/ against the deterministic inclusion rule
// (document:project/project-substrate-inclusion) and flags drift against the
// materialised .claude/skills tree. It is a pure read — no dispatch, no
// writes — so a re-run on the same inputs yields the same verdict, which is
// what lets `--dry-run` use it as a standalone check (sty_50ecb56f). An empty
// result means the tree is clean.
//
// Checks, all mechanical (no judgement call):
//   - layout is flat .satellites/<kind>/<name>.md (no nesting);
//   - frontmatter `type:`, when set, matches the kind dir's mapped type
//     (a skills/ file may not declare type:document, etc.);
//   - frontmatter scope, when set, is "project" (workspace is not supported);
//   - frontmatter workspace_id is never set (no workspace concept here);
//   - frontmatter project_id, when set, matches the repo project_id;
//   - a skills/ file carries the required `name` + `description` + `kind`
//     frontmatter (and `applies_to` for workflows);
//   - drift: a project-scoped, stamped (sync-materialised) skill in
//     .claude/skills/ with no .satellites/skills/ source. System/workspace
//     stamped skills are owned by another scope (sync materialises them here
//     with no project source) and are exempt; unstamped local skills are
//     operator-owned and ignored.
func validateUpload(rootForKind func(kind string) string, skillsRoot, projectID string) ([]violation, error) {
	var vs []violation
	configSkillNames := map[string]bool{}

	for kindDir, defaultType := range kindDirs {
		kindRoot := filepath.Join(rootForKind(kindDir), kindDir)
		info, err := os.Stat(kindRoot)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("validate: stat %s: %w", kindRoot, err)
		}
		if !info.IsDir() {
			vs = append(vs, violation{kindRoot, "kind-dir", fmt.Sprintf("%q is not a directory", kindDir)})
			continue
		}

		walkErr := filepath.WalkDir(kindRoot, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
				return nil
			}

			rel, relErr := filepath.Rel(kindRoot, p)
			if relErr != nil {
				return relErr
			}
			if strings.Contains(filepath.ToSlash(rel), "/") {
				vs = append(vs, violation{p, "layout", "unexpected nesting — expected .satellites/<kind>/<name>.md (flat, project scope)"})
				return nil
			}
			filename := rel

			_, _, fm, ferr := readFileWithFrontmatter(p)
			if ferr != nil {
				vs = append(vs, violation{p, "frontmatter", ferr.Error()})
				return nil
			}

			if t := strings.TrimSpace(fm.Type); t != "" && t != defaultType {
				vs = append(vs, violation{p, "type-mismatch", fmt.Sprintf("frontmatter type:%q under %s/ — expected %q", t, kindDir, defaultType)})
			}
			if s := strings.TrimSpace(fm.Scope); s != "" && s != "project" {
				vs = append(vs, violation{p, "scope-unsupported", fmt.Sprintf("frontmatter scope:%q — only project scope is supported (workspace is not yet supported)", s)})
			}
			if w := strings.TrimSpace(fm.WorkspaceID); w != "" {
				vs = append(vs, violation{p, "workspace-unsupported", "frontmatter workspace_id is not supported — repo-local substrate is project scope only"})
			}
			if pj := strings.TrimSpace(fm.ProjectID); pj != "" && pj != projectID {
				vs = append(vs, violation{p, "project-mismatch", fmt.Sprintf("frontmatter project_id:%q != repo project_id %q (.satellites/satellites.toml)", pj, projectID)})
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
					vs = append(vs, violation{p, "skill-dispatch", "skill missing required frontmatter: kind (workflow|function|gate|reviewer|capability|task)"})
				case !validSkillKinds[k]:
					vs = append(vs, violation{p, "skill-dispatch", fmt.Sprintf("skill kind:%q is not one of workflow|function|gate|reviewer|capability|task", k)})
				case k == "workflow" && len(fm.AppliesTo) == 0:
					vs = append(vs, violation{p, "skill-dispatch", "workflow skill missing required frontmatter: applies_to"})
				}
				// Workflow-review drift gate (sty_1604064f): when a kind:workflow
				// skill changes, validate that the engagement lifecycle's
				// derivations still map — a terminal state is identifiable and an
				// editable phase is derivable. A broken workflow fails the upload
				// LOUDLY here rather than silently mis-gating at runtime.
				if strings.TrimSpace(fm.Kind) == "workflow" && len(fm.AppliesTo) > 0 {
					if raw, rerr := os.ReadFile(p); rerr == nil {
						if wf, perr := workflow.Parse(raw); perr == nil {
							if lerr := wf.ValidateLifecycle(); lerr != nil {
								vs = append(vs, violation{p, "workflow-lifecycle", lerr.Error()})
							}
						}
					}
				}
				// Claim both name forms: sync materialises under the
				// satellites-prefixed name, so an unprefixed source (e.g.
				// urgent-workflow.md) must still own its prefixed local copy —
				// while a hand-placed unprefixed local copy stays claimed too.
				resolved := resolveName(filename, fm.Name)
				configSkillNames[resolved] = true
				configSkillNames[localSkillName(resolved)] = true
			}
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	// Drift: a project-scoped, stamped skill on disk with no source under
	// .satellites/skills/. System/workspace stamped skills are exempt — sync
	// materialises them here and they carry no project source by design.
	stamped, serr := readStampedLocalSkills(skillsRoot)
	if serr != nil {
		return nil, fmt.Errorf("validate: scan %s: %w", skillsRoot, serr)
	}
	for _, l := range stamped {
		// Only a project-scoped stamped skill is expected to have a top-level
		// skills/ source. A system- or workspace-scoped skill is materialised
		// here by `skill sync` (which reconciles every scope the repo can see)
		// and legitimately has no project source — flagging it is a false
		// positive. Scope empty/unset is treated as project, so a real project
		// orphan still fails closed.
		if sc := strings.TrimSpace(l.Scope); sc != "" && sc != "project" {
			continue
		}
		// A LIBRARY-PINNED skill (carries a publisher in its stamp) is CONSUMED
		// via library_pins — it has no local source by design (epic:skills-
		// registry consumer model). Exempt it like a system/workspace skill;
		// otherwise a consumer repo whose governance is registry-pinned cannot
		// upload any document (sty_a75dd8c5 dogfood exposed this).
		if strings.TrimSpace(l.Stamp.Publisher) != "" {
			continue
		}
		if !configSkillNames[l.Name] {
			vs = append(vs, violation{
				filepath.Join(skillsRoot, l.Name),
				"orphan-skill",
				"project-owned (stamped) skill in .claude/skills/ with no top-level skills/ source",
			})
		}
	}
	return vs, nil
}

// classifyDocumentFile builds a project-scoped target from a flat
// .satellites/<kind>/<name>.md source. The kind is fixed by the directory
// the walker is in; the project_id is the repo's (from satellites.toml) —
// neither is read from frontmatter. The default upsert type is the kind's
// mapped type, overridable by `type:` in frontmatter.
func classifyDocumentFile(filePath, kind, projectID string) documentTarget {
	defaultType := kindDirs[kind]
	raw, body, fm, err := readFileWithFrontmatter(filePath)
	if err != nil {
		// A read/parse failure here is impossible in practice: validateUpload
		// has already parsed every file and refused the push on error. Fall
		// back to an empty body rather than panicking.
		return documentTarget{Path: filePath, ProjectID: projectID, Name: resolveName(filepath.Base(filePath), ""), Type: defaultType}
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
		Path:      filePath,
		ProjectID: projectID,
		Name:      resolveName(filepath.Base(filePath), fm.Name),
		Type:      docType,
		Tags:      fm.Tags,
		Headline:  strings.TrimSpace(fm.Headline),
		Level:     strings.TrimSpace(fm.Level),
		Scope:     strings.TrimSpace(fm.Scope),
		Body:      storedBody,
		Raw:       raw,
	}
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

// uploadLabel renders the dispatch target as `project/<project_id>/<name>`
// for the per-file progress line.
func uploadLabel(t documentTarget) string {
	return fmt.Sprintf("project/%s/%s", t.ProjectID, t.Name)
}

// marshalUpsertRequest builds the JSON document_upsert payload for a
// single file. Every upload is project-scoped with the repo's project_id;
// workspace_id is intentionally omitted — the server derives it from the
// project (sty_afc0769c). Tags are passed through as-is; the request omits
// the tag pointer when no tags are declared (frontmatter absent), matching
// the document_upsert "leave alone" semantics for that field.
func marshalUpsertRequest(t documentTarget) (json.RawMessage, error) {
	docType := t.Type
	if docType == "" {
		docType = "document"
	}
	payload := map[string]any{
		"type":       docType,
		"scope":      "project",
		"project_id": t.ProjectID,
		"name":       t.Name,
		"body":       t.Body,
	}
	if t.Tags != nil {
		payload["tags"] = t.Tags
	}
	// Send headline only when the frontmatter declared one — an omitted key
	// leaves the stored value alone (matches the tag "leave alone" semantics).
	// Story-2 generation fills it when absent; here we never clobber with "".
	if t.Headline != "" {
		payload["headline"] = t.Headline
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
