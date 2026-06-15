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
				return publishSkill(ctx, out, args[0], *configArg, *userArg, dryRun, skipReview)
			}
			var (
				names []string
				err   error
			)
			if all {
				names, err = allSkillNames()
			} else {
				names, err = changedSkillNames(changedSince)
			}
			if err != nil {
				return err
			}
			return publishBatch(ctx, out, names, *configArg, *userArg, dryRun, skipReview)
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
func publishBatch(ctx context.Context, out io.Writer, names []string, configArg, userArg string, dryRun, skipReview bool) error {
	if len(names) == 0 {
		fmt.Fprintln(out, "no skills to publish")
		return nil
	}
	var failures int
	for _, name := range names {
		if err := publishSkill(ctx, out, name, configArg, userArg, dryRun, skipReview); err != nil {
			fmt.Fprintf(out, "✗ %s: %v\n", name, err)
			failures++
		}
	}
	if failures > 0 {
		return fmt.Errorf("publish: %d of %d skill(s) failed", failures, len(names))
	}
	fmt.Fprintf(out, "published %d skill(s)\n", len(names))
	return nil
}

// allSkillNames returns every skill base name under .satellites/skills/, sorted.
func allSkillNames() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(substrateRoot, "skills", "*.md"))
	if err != nil {
		return nil, fmt.Errorf("publish: scan skills: %w", err)
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, strings.TrimSuffix(filepath.Base(m), ".md"))
	}
	sort.Strings(names)
	return names, nil
}

// changedSkillNames returns the base names of skill files that differ from ref,
// keeping only files that still exist (a deletion cannot be published). A git
// failure (e.g. an unknown ref) is an error, distinct from an empty diff.
func changedSkillNames(ref string) ([]string, error) {
	skillsDir := filepath.Join(substrateRoot, "skills")
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

// publishSkill resolves, reviews, stamps, and dispatches one library publish.
func publishSkill(ctx context.Context, out io.Writer, name, configArg, userArg string, dryRun, skipReview bool) error {
	publisher, err := projectIDFromConfig(configArg)
	if err != nil {
		return err
	}
	path := filepath.Join(substrateRoot, "skills", name+".md")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("publish: no skill at %s — author it under %s/skills/ first", path, substrateRoot)
	}
	t := classifyDocumentFile(path, "skills", publisher)
	if strings.TrimSpace(t.Body) == "" {
		return fmt.Errorf("publish: %s is empty or unparseable", path)
	}
	if t.Type != "skill" {
		return fmt.Errorf("publish: %s declares type %q — only a skill can be published to the library", path, t.Type)
	}
	if t.Name != name {
		return fmt.Errorf("publish: %s declares name %q — rename the file or the frontmatter so they agree", path, t.Name)
	}

	// The same strict review that gates upload (AC2): a drift-prone
	// reference blocks before any dispatch.
	if !skipReview && !hasReviewExemptTag(t.Tags) {
		findings := reviewContent(t.Body)
		findings = append(findings, reviewSkillSelfContained(t.Body)...)
		if len(findings) > 0 {
			fmt.Fprintf(out, "content-review blocked %s — %d drift-prone reference(s); run skill %q for the maintainability critique, or pass --skip-review:\n",
				path, len(findings), reviewSkillForKind("skills"))
			for _, f := range findings {
				fmt.Fprintf(out, "  ✗ %s\n", f.String())
			}
			return fmt.Errorf("%s: content review blocked %d drift-prone reference(s) (override with --skip-review)", path, len(findings))
		}
	}

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
		"type":       "skill",
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
	resp, err := dispatchVerb(ctx, "document_upsert", req, configArg, userArg)
	if err != nil {
		return fmt.Errorf("publish %s: %w", path, err)
	}
	fmt.Fprintf(out, "%s → library/%s/%s (%s)\n", path, publisher, name, summariseUploadResp(resp))
	return nil
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
