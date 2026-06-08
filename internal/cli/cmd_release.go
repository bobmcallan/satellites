// `satellites release check` — the local mirror of the CI release gate
// (sty_686cd836). The release workflow fails LOUD when client-affecting code
// changed since the `v<satellites.version>` tag without a `satellites.version`
// bump (the guard from sty_5a97504a). That failure only surfaces post-push, in
// a workflow separate from test+deploy, so it is easy to miss. This gate runs
// the SAME CLIENT_PATHS diff locally — wired into the commit-push routine after
// the commit, before the push — so "client code changed, version not bumped" is
// caught before it becomes a red release.
//
// Pure-core + IO-shell, mirroring the techdebt/surface gates. No new MCP verb.

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// releaseClientPaths is the set of paths the CI release gate treats as
// client-affecting — kept verbatim in step with
// .github/workflows/release.yml's CLIENT_PATHS. A change under any of these
// since the released tag requires a satellites.version bump.
var releaseClientPaths = []string{
	"internal/cli/",
	"cmd/satellites/",
	"internal/verb/",
	"internal/workstate/",
	"internal/workflow/",
	"internal/audit/",
}

func init() {
	root := &cobra.Command{
		Use:   "release",
		Short: "Release gating (mirror the CI client-path / satellites.version check locally)",
	}
	root.AddCommand(newReleaseCheckCmd())
	register(root)
}

func newReleaseCheckCmd() *cobra.Command {
	var worktree string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Fail closed when client code changed without a satellites.version bump",
		Long: `check mirrors the CI release gate locally.

It derives TAG=v<satellites.version> from .version. If that tag does not exist,
satellites.version was already bumped and the release will cut it — CLEAN. If the
tag exists, it diffs the client paths since the tag (the same CLIENT_PATHS the
release workflow uses); any change there means client code is being pushed
without a release, so it BLOCKS (exit 1) — bump satellites.version.

Run it in the commit-push routine after the commit and before the push, so a
missing version bump is caught locally instead of as a post-push red release.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runReleaseCheck(ctx, releaseOpts{
				Worktree: strings.TrimSpace(worktree),
				Stdout:   cmd.OutOrStdout(),
				Stderr:   cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&worktree, "worktree", "", "Repo root the check runs in (default: current directory).")
	return cmd
}

type releaseOpts struct {
	Worktree string
	Stdout   io.Writer
	Stderr   io.Writer
}

func runReleaseCheck(ctx context.Context, opts releaseOpts) error {
	dir := opts.Worktree
	if dir == "" {
		dir = "."
	}

	versionBody, err := os.ReadFile(dir + "/.version")
	if err != nil {
		return fmt.Errorf("release gate: read .version: %w", err)
	}
	version, err := parseSatellitesVersion(string(versionBody))
	if err != nil {
		return fmt.Errorf("release gate: %w", err)
	}
	tag := "v" + version

	// Whether the release tag already exists is authoritative on the REMOTE
	// (CI creates it there). A locally-stale repo would otherwise report a
	// false CLEAN. Resolve the tag's commit from origin; fall back to a local
	// tag when offline.
	ref, tagExists := resolveReleasedTag(ctx, dir, tag)
	var changed []string
	if tagExists {
		changed = gitChangedClientPaths(ctx, dir, ref)
	}

	ok, report := releaseCheckVerdict(version, tagExists, changed)
	fmt.Fprint(opts.Stdout, report)
	if !ok {
		return fmt.Errorf("release gate: client code changed since %s without a satellites.version bump — bump .version (satellites.version)", tag)
	}
	return nil
}

// releaseCheckVerdict is the pure core: given the CLI version, whether its tag
// already exists, and the client paths changed since that tag, decide whether
// the commit may proceed and render the report.
func releaseCheckVerdict(version string, tagExists bool, changed []string) (bool, string) {
	var b strings.Builder
	b.WriteString("\n── release gate ──\n")
	tag := "v" + version
	if !tagExists {
		b.WriteString(fmt.Sprintf("satellites.version %s is unreleased — the release workflow will cut tag %s.\n", version, tag))
		b.WriteString("verdict: CLEAN — version bumped; commit may proceed.\n")
		return true, b.String()
	}
	if len(changed) == 0 {
		b.WriteString(fmt.Sprintf("tag %s exists and no client paths changed since it.\n", tag))
		b.WriteString("verdict: CLEAN — no client release needed; commit may proceed.\n")
		return true, b.String()
	}
	b.WriteString(fmt.Sprintf("CLIENT CODE CHANGED since %s but satellites.version was not bumped:\n", tag))
	for _, p := range changed {
		b.WriteString("  ✗ " + p + "\n")
	}
	b.WriteString("verdict: BLOCKED — bump satellites.version (the CLI release tag) to release these, then `satellites update`.\n")
	return false, b.String()
}

// parseSatellitesVersion pulls the satellites.version field out of a .version
// file body (mirrors the awk the workflows use).
func parseSatellitesVersion(body string) (string, error) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "satellites.version:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "satellites.version:"))
			if v == "" {
				return "", fmt.Errorf(".version: satellites.version is empty")
			}
			return v, nil
		}
	}
	return "", fmt.Errorf(".version: satellites.version field not found")
}

// resolveReleasedTag reports whether the release tag exists and returns the tag
// name to diff against. CI creates the tag on origin (often annotated), so we
// fetch it locally first — that brings the tag and its target commit into the
// local object store and lets `git diff <tag>..HEAD` deref it. The fetch is
// force (+) so a stale local tag is reconciled to origin, and harmless when the
// tag does not exist on origin or we are offline (we then fall back to any
// existing local tag). Returns ("", false) when the version is unreleased.
func resolveReleasedTag(ctx context.Context, dir, tag string) (string, bool) {
	fetch := exec.CommandContext(ctx, "git", "fetch", "--quiet", "origin",
		"+refs/tags/"+tag+":refs/tags/"+tag)
	fetch.Dir = dir
	_ = fetch.Run() // best-effort: no such tag / offline → fall through to the local check

	chk := exec.CommandContext(ctx, "git", "rev-parse", "-q", "--verify", "refs/tags/"+tag)
	chk.Dir = dir
	if chk.Run() == nil {
		return tag, true
	}
	return "", false
}

func gitChangedClientPaths(ctx context.Context, dir, ref string) []string {
	args := append([]string{"diff", "--name-only", ref + "..HEAD", "--"}, releaseClientPaths...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var changed []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			changed = append(changed, l)
		}
	}
	return changed
}
