// `satellites skill publish` — promote one .satellites/skills/ skill into
// the shared library under this repo's publisher namespace
// (epic:skill-library, sty_d971a478).
//
// Publishing is a deliberate act distinct from the authoring loop: upload
// keeps iterating the project scope; publish dispatches a single
// document_upsert at scope=library, gated by the same strict content review
// as upload, stamped with the publishing context (publisher project, repo
// URL, commit). Headless by construction — dispatchVerb authenticates with
// the credential-store API key and nothing on the path prompts — so a CI
// step can publish on merge. No new MCP verbs.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// libraryProvenance is the publishing context stamped into the published
// body. Publisher is the repo's project id; Repo/Commit are read from the
// repo's git state at publish time, best-effort (empty when unavailable).
type libraryProvenance struct {
	Publisher string `json:"publisher"`
	Repo      string `json:"repo,omitempty"`
	Commit    string `json:"commit,omitempty"`
}

const (
	libraryStampBegin = "<!-- satellites-library:begin"
	libraryStampEnd   = "satellites-library:end -->"
)

// newSkillPublishCmd builds the `skill publish <name>` command.
func newSkillPublishCmd(configArg, userArg *string) *cobra.Command {
	var (
		dryRun       bool
		skipReview   bool
		all          bool
		changedSince string
	)
	cmd := &cobra.Command{
		Use:   "publish [name]",
		Short: "Promote .satellites/skills/ skill(s) into the shared library under this project's publisher namespace",
		Long: `publish promotes a skill (or a batch) into the shared library.

  publish <name>                 one named skill
  publish --all                  every skill under .satellites/skills/
  publish --changed-since <ref>  only skills whose files differ from <ref>

A batch reuses the single-publish path per skill (review + library stamp +
provenance), prints a per-skill outcome line, and exits non-zero if any skill
fails — the others still publish. A batch with nothing to publish is a clean
no-op (exit 0). --dryrun and --skip-review apply across the batch.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			// Exactly one selector: a name, --all, or --changed-since.
			selectors := 0
			if len(args) == 1 {
				selectors++
			}
			if all {
				selectors++
			}
			if strings.TrimSpace(changedSince) != "" {
				selectors++
			}
			switch {
			case selectors == 0:
				return fmt.Errorf("publish: provide a skill <name>, --all, or --changed-since <ref>")
			case selectors > 1:
				return fmt.Errorf("publish: <name>, --all, and --changed-since are mutually exclusive")
			case len(args) == 1:
				return publishSkill(ctx, out, args[0], "skills", *configArg, *userArg, dryRun, skipReview)
			}
			var (
				names []string
				err   error
			)
			if all {
				names, err = allPublishableNames("skills", *configArg)
			} else {
				names, err = changedPublishableNames(changedSince, "skills", *configArg)
			}
			if err != nil {
				return err
			}
			return publishBatch(ctx, out, names, "skills", *configArg, *userArg, dryRun, skipReview)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dryrun", false, "Print the identity, version, and provenance that would be published — dispatch nothing")
	cmd.Flags().BoolVar(&skipReview, "skip-review", false, "Skip the strict content review (drift-prone reference check) — use only after running the per-type review skill")
	cmd.Flags().BoolVar(&all, "all", false, "Publish every skill under .satellites/skills/")
	cmd.Flags().StringVar(&changedSince, "changed-since", "", "Publish only skills whose files differ from the given git ref")
	return cmd
}

// publishBatch publishes each named skill through the single-publish path,
// reporting per-skill outcomes. A skill's failure is recorded and printed but
// does not stop the batch; any failure makes the batch exit non-zero. An empty
// set is a clean no-op (exit 0).
func publishBatch(ctx context.Context, out io.Writer, names []string, kind, configArg, userArg string, dryRun, skipReview bool) error {
	_, noun, ok := publishableKind(kind)
	if !ok {
		return fmt.Errorf("publish: unknown kind %q — expected skills or tasks", kind)
	}
	if len(names) == 0 {
		fmt.Fprintf(out, "no %ss to publish\n", noun)
		return nil
	}
	var failures int
	for _, name := range names {
		if err := publishSkill(ctx, out, name, kind, configArg, userArg, dryRun, skipReview); err != nil {
			fmt.Fprintf(out, "✗ %s: %v\n", name, err)
			failures++
		}
	}
	if failures > 0 {
		return fmt.Errorf("publish: %d of %d %s(s) failed", failures, len(names), noun)
	}
	fmt.Fprintf(out, "published %d %s(s)\n", len(names), noun)
	return nil
}

// allPublishableNames returns every base name under the configured authoring
// root for a kind (default .satellites/<kind>/), sorted.
func allPublishableNames(kind, configArg string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(resolveSubstrateRoot(kind, configArg), kind, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("publish: scan %s: %w", kind, err)
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, strings.TrimSuffix(filepath.Base(m), ".md"))
	}
	sort.Strings(names)
	return names, nil
}

// changedPublishableNames returns the base names of a kind's files that differ
// from ref, keeping only files that still exist (a deletion cannot be
// published). A git failure (e.g. an unknown ref) is an error, distinct from an
// empty diff.
func changedPublishableNames(ref, kind, configArg string) ([]string, error) {
	skillsDir := filepath.Join(resolveSubstrateRoot(kind, configArg), kind)
	outBytes, err := exec.Command("git", "diff", "--name-only", ref, "--", skillsDir).Output()
	if err != nil {
		return nil, fmt.Errorf("publish: git diff against %q: %w", ref, err)
	}
	seen := map[string]bool{}
	names := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(outBytes)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasSuffix(line, ".md") {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(line), ".md")
		if seen[name] {
			continue
		}
		if _, e := os.Stat(filepath.Join(skillsDir, name+".md")); e != nil {
			continue // deleted/renamed-away — nothing to publish
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// resolvePublishScope maps an artifact's DECLARED level (preferred) or explicit
// scope to the storage scope `skill publish` routes to (epic:client-dir-separation
// order-4): global→library (the shared surface), project→project, system→REFUSED
// (built-in, never user-published). An empty declaration defaults to library for
// back-compat (the pre-order-4 behaviour). Pure; unit-tested (AC2).
func resolvePublishScope(level, scope string) (string, error) {
	pick := strings.ToLower(strings.TrimSpace(level))
	if pick == "" {
		pick = strings.ToLower(strings.TrimSpace(scope))
	}
	switch pick {
	case "", "global", "library":
		return "library", nil
	case "project":
		return "project", nil
	case "system":
		return "", fmt.Errorf("system is built-in and is never user-published — declare level global (shared) or project (project-scoped)")
	default:
		return "", fmt.Errorf("unknown level/scope %q — use system, project, or global", pick)
	}
}

// publishableKind maps a publishable substrate kind ("skills" | "tasks") to the
// document type it carries and a singular noun for messages. A task republishes
// the skill register's library machinery (epic:global-tasks) — same scope,
// provenance stamp, and headless dispatch; only the source dir, the expected
// type, and (for the review/attestation barrier) the behaviour-kind class differ.
func publishableKind(kind string) (typ, noun string, ok bool) {
	switch kind {
	case "skills":
		return "skill", "skill", true
	case "tasks":
		return "task", "task", true
	default:
		return "", "", false
	}
}

// publishSkill resolves, reviews, stamps, and dispatches one publish, ROUTED by
// the artifact's declared level (order-4): a global artifact lands on the shared
// library surface (publisher-stamped), a project artifact stays project-scoped,
// system is refused. The kind ("skills" | "tasks") selects the source dir and the
// expected type; tasks reuse the same library/provenance/dispatch path as skills
// (epic:global-tasks) but are NOT a behaviour kind, so they skip the
// skill-self-contained critique and the review attestation.
func publishSkill(ctx context.Context, out io.Writer, name, kind, configArg, userArg string, dryRun, skipReview bool) error {
	wantType, noun, ok := publishableKind(kind)
	if !ok {
		return fmt.Errorf("publish: unknown kind %q — expected skills or tasks", kind)
	}
	publisher, err := projectIDFromConfig(configArg)
	if err != nil {
		return err
	}
	root := resolveSubstrateRoot(kind, configArg)
	path := filepath.Join(root, kind, name+".md")
	if _, err := os.Stat(path); err != nil {
		nudgeStaleSubstrateDir(out, kind, root)
		return fmt.Errorf("publish: no %s at %s — author it under the configured %s/ folder first", noun, path, kind)
	}
	t := classifyDocumentFile(path, kind, publisher)
	if strings.TrimSpace(t.Body) == "" {
		return fmt.Errorf("publish: %s is empty or unparseable", path)
	}
	if t.Type != wantType {
		return fmt.Errorf("publish: %s declares type %q — only a %s can be published from %s/", path, t.Type, noun, kind)
	}
	if t.Name != name {
		return fmt.Errorf("publish: %s declares name %q — rename the file or the frontmatter so they agree", path, t.Name)
	}

	// The same strict review that gates upload (AC2): a drift-prone
	// reference blocks before any dispatch. The skill-self-contained critique
	// applies only to skills (a behaviour kind materialised as a runnable
	// SKILL.md); a task is plain prose, so it gets only the shared drift check.
	if !skipReview && !hasReviewExemptTag(t.Tags) {
		findings := reviewContent(t.Body)
		if kind == "skills" {
			findings = append(findings, reviewSkillSelfContained(t.Body)...)
		}
		if len(findings) > 0 {
			fmt.Fprintf(out, "content-review blocked %s — %d drift-prone reference(s); run skill %q for the maintainability critique, or pass --skip-review:\n",
				path, len(findings), reviewSkillForKind(kind))
			for _, f := range findings {
				fmt.Fprintf(out, "  ✗ %s\n", f.String())
			}
			return fmt.Errorf("%s: content review blocked %d drift-prone reference(s) (override with --skip-review)", path, len(findings))
		}
	}

	pubScope, err := resolvePublishScope(t.Level, t.Scope)
	if err != nil {
		return fmt.Errorf("publish %s: %w", path, err)
	}
	if pubScope == "project" {
		return publishProjectScoped(ctx, out, t.Body, name, publisher, configArg, userArg, dryRun)
	}
	// pubScope == "library" → the shared/global surface (provenance-stamped).
	prov := libraryProvenance{
		Publisher: publisher,
		Repo:      gitOutput("remote", "get-url", "origin"),
		Commit:    gitOutput("rev-parse", "HEAD"),
	}
	stamp, err := renderLibraryStamp(prov)
	if err != nil {
		return fmt.Errorf("publish: render provenance: %w", err)
	}
	body := injectLibraryStamp(t.Body, stamp)

	if dryRun {
		next := nextLibraryVersion(ctx, publisher, name, configArg, userArg)
		fmt.Fprintf(out, "[dryrun] would publish %s/%s\n", publisher, name)
		fmt.Fprintf(out, "[dryrun]   version: %d\n", next)
		fmt.Fprintf(out, "[dryrun]   provenance: %s\n", stamp)
		fmt.Fprintln(out, "[dryrun] nothing dispatched")
		return nil
	}

	payload := map[string]any{
		"type":       t.Type,
		"scope":      "library",
		"project_id": publisher,
		"name":       name,
		"body":       body,
	}
	if t.Tags != nil {
		payload["tags"] = t.Tags
	}
	if t.Headline != "" {
		payload["headline"] = t.Headline
	}
	req, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	// A skill is a behaviour kind: bind a review attestation to the stamped
	// library body so the behaviour-kind verb gate (sty_e6226180) accepts the
	// promotion rather than refusing it as an unreviewed write. A task is NOT a
	// behaviour kind (verb.reviewRequiredKind == false), so the gate does not
	// demand one — publishing it needs no attestation.
	if t.Type == "skill" {
		if req, err = attestReview(req, reviewSkillForKind(kind), body); err != nil {
			return fmt.Errorf("publish: %w", err)
		}
	}
	resp, err := dispatchVerb(ctx, "document_upsert", req, configArg, userArg)
	if err != nil {
		return fmt.Errorf("publish %s: %w", path, err)
	}
	fmt.Fprintf(out, "%s → library/%s/%s (%s)\n", path, publisher, name, summariseUploadResp(resp))
	return nil
}

// publishProjectScoped upserts a project-level skill to its project scope (no
// library provenance stamp — it is not shared). Project scope requires both
// workspace_id and project_id, so it resolves the publisher project's workspace
// via project_get (order-4 AC2). Used when an artifact declares level: project.
func publishProjectScoped(ctx context.Context, out io.Writer, body, name, projectID, configArg, userArg string, dryRun bool) error {
	wsID, err := workspaceForProject(ctx, projectID, configArg, userArg)
	if err != nil {
		return fmt.Errorf("publish project skill %q: resolve workspace: %w", name, err)
	}
	if dryRun {
		fmt.Fprintf(out, "[dryrun] would publish project/%s/%s (workspace %s)\n", projectID, name, wsID)
		return nil
	}
	req, err := json.Marshal(map[string]any{
		"type": "skill", "scope": "project", "project_id": projectID, "workspace_id": wsID, "name": name, "body": body,
	})
	if err != nil {
		return err
	}
	resp, err := dispatchVerb(ctx, "document_upsert", req, configArg, userArg)
	if err != nil {
		return fmt.Errorf("publish project skill %q: %w", name, err)
	}
	fmt.Fprintf(out, "project/%s/%s (%s)\n", projectID, name, summariseUploadResp(resp))
	return nil
}

// workspaceForProject resolves a project's workspace_id via project_get — needed
// to write a project-scoped row (which requires both ids).
func workspaceForProject(ctx context.Context, projectID, configArg, userArg string) (string, error) {
	req, err := json.Marshal(map[string]any{"id": projectID})
	if err != nil {
		return "", err
	}
	raw, err := dispatchVerb(ctx, "project_get", req, configArg, userArg)
	if err != nil {
		return "", err
	}
	var got struct {
		Project struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"project"`
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return "", err
	}
	if got.Project.WorkspaceID != "" {
		return got.Project.WorkspaceID, nil
	}
	if got.WorkspaceID != "" {
		return got.WorkspaceID, nil
	}
	return "", fmt.Errorf("project_get returned no workspace_id for %s", projectID)
}

// nextLibraryVersion reads the library row's current latest_version and
// returns the version a publish would land (1 for a new row). Read-only —
// safe under --dryrun; any error (including not-found) reports a fresh row.
func nextLibraryVersion(ctx context.Context, publisher, name, configArg, userArg string) int {
	req, err := json.Marshal(map[string]any{
		"scope": "library", "project_id": publisher, "name": name,
	})
	if err != nil {
		return 1
	}
	resp, err := dispatchVerb(ctx, "document_get", req, configArg, userArg)
	if err != nil {
		return 1
	}
	var parsed struct {
		Document struct {
			LatestVersion int `json:"latest_version"`
		} `json:"document"`
	}
	if json.Unmarshal(resp, &parsed) != nil {
		return 1
	}
	return parsed.Document.LatestVersion + 1
}

// renderLibraryStamp serialises the provenance as the single-line comment
// stamp carried in the published body — the same shape as the sync stamp,
// machine-readable by adopt and sync-pin consumers, never authored content.
func renderLibraryStamp(p libraryProvenance) (string, error) {
	blob, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s %s", libraryStampBegin, blob, libraryStampEnd), nil
}

// libraryStampLine matches a whole stamp line (with surrounding newline)
// anywhere in a body, so re-publish replaces rather than accumulates.
var libraryStampLine = regexp.MustCompile(`(?m)^<!-- satellites-library:begin .* satellites-library:end -->\n?`)

// injectLibraryStamp returns body with exactly one provenance stamp, placed
// on the line after the frontmatter close (mirroring the sync stamp's
// current layout) or at the top when the body carries no frontmatter.
// Idempotent: any prior stamp is stripped first.
func injectLibraryStamp(body, stamp string) string {
	body = libraryStampLine.ReplaceAllString(body, "")
	if strings.HasPrefix(body, "---\n") || strings.HasPrefix(body, "---\r\n") {
		rest := body[strings.IndexByte(body, '\n')+1:]
		if idx := strings.Index(rest, "\n---"); idx >= 0 {
			closeEnd := strings.IndexByte(rest[idx+1:], '\n')
			if closeEnd >= 0 {
				at := len(body) - len(rest) + idx + 1 + closeEnd + 1
				return body[:at] + stamp + "\n" + body[at:]
			}
		}
	}
	return stamp + "\n" + body
}

// gitOutput runs git in the working tree and returns its trimmed stdout,
// or "" when git/the value is unavailable — provenance is best-effort.
func gitOutput(args ...string) string {
	outBytes, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(outBytes))
}
