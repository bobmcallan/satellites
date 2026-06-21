// `satellites clear` — scoped archive (soft-tombstone) of a project's
// governance rows (principles, skills, and the kind:workflow/gate/reviewer
// skills). It exists because the substrate's delete is a SOFT-tombstone the CLI
// otherwise exposed only as a one-row-at-a-time `exec document_delete`; clearing
// a project's governance for a clean rebuild meant scripting that raw verb
// around a surface that deliberately ships no `delete` command. `clear` makes
// the operation first-class WITHOUT adding a server-side discriminator: it is a
// pure client-side COMPOSITION of the existing document_list + document_delete
// verbs (the `no-new-mcp-verbs` principle — nouns/lifecycle are CLI ergonomics).
//
// Three rails keep it safe:
//   - PROJECT scope only. Enumeration lists the caller's configured project and
//     never the system/shared scope, so rows like `agent-goals` or
//     `satellites-story-summary` can never be targeted.
//   - STORIES are never touched. The kind sets resolve to principles + skills
//     (type:skill); a story would hard-delete irreversibly, so any row whose
//     type is `story` is defensively dropped from the kill-list.
//   - DRY-RUN + CONFIRM. `--dryrun` enumerates and writes nothing; a real run
//     prints the kill-list and requires explicit confirmation before any delete.
//     Tombstones are recoverable (an empty-body version), and local
//     `.satellites/**` sources are untouched.

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// clearKinds maps the --kind selector to the substrate set it clears. `skill`
// is the full type:skill set (which includes the kind:workflow/gate/reviewer
// governance skills); `workflow` narrows to just the kind:workflow definitions.
var clearKinds = []string{"principle", "skill", "workflow", "all"}

func init() {
	var (
		configArg string
		userArg   string
		kindArg   string
		dryrun    bool
		force     bool
		assumeYes bool
	)
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Archive (soft-tombstone) this project's principles / skills / workflows",
		Long: `clear soft-tombstones the caller's PROJECT-scoped governance rows so the
project can be rebuilt on an updated satellites. It is a client-side composition
of document_list + document_delete — it adds no MCP verb.

Pick what to clear with --kind:
  principle   principles (rows tagged principles:*)
  skill       every project skill (includes the kind:workflow/gate/reviewer skills)
  workflow    only the kind:workflow definitions
  all         principles + every project skill

Rails: project scope only (never system/shared rows like agent-goals); stories
are never touched (they hard-delete); --dryrun enumerates without writing.

Confirmation is TTY-aware. At an interactive terminal a real run prompts [y/N].
When stdin is NOT a terminal (an agent via its harness, CI, a pipe) the CLI
cannot prompt, so clear REFUSES rather than guess — re-run with --force. For an
agent the operator's real confirmation gate is the harness permission prompt on
this command, not a CLI prompt; the agent passes --force once approved.

Tombstones are recoverable and local .satellites/ sources are left on disk.
Refresh with ` + "`satellites skill sync`" + ` afterwards.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClear(cmd.OutOrStdout(), cmd.InOrStdin(), kindArg, dryrun, force || assumeYes, configArg, userArg)
		},
	}
	cmd.Flags().StringVar(&kindArg, "kind", "", "What to clear: principle | skill | workflow | all (required)")
	cmd.Flags().BoolVar(&dryrun, "dryrun", false, "Enumerate the kill-list and write nothing")
	cmd.Flags().BoolVar(&force, "force", false, "Delete without confirmation. REQUIRED when stdin is not an interactive terminal (agent/CI): there the CLI cannot prompt, so the operator's gate is the harness permission prompt on this command.")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Alias of --force (retained for back-compat).")
	cmd.Flags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	cmd.Flags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID).")
	register(cmd)
}

// runClear resolves the caller's project, enumerates the requested kill-list,
// and (outside --dryrun, after confirmation) soft-tombstones each row. `force`
// is the resolved --force/--yes bypass; interactivity is detected from stdin.
func runClear(out io.Writer, in io.Reader, kind string, dryrun, force bool, configArg, userArg string) error {
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind == "" {
		return fmt.Errorf("clear: --kind is required (one of: %s)", strings.Join(clearKinds, ", "))
	}
	if !clearKindValid(clearKinds, kind) {
		return fmt.Errorf("clear: unknown --kind %q (want one of: %s)", kind, strings.Join(clearKinds, ", "))
	}

	ctx := context.Background()
	projectID, err := projectIDFromConfig(configArg)
	if err != nil || strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("clear: no project configured — clear operates on the project in .satellites/satellites.toml")
	}
	wsID, err := resolveWorkspaceID(ctx, projectID, configArg, userArg)
	if err != nil {
		return fmt.Errorf("clear: resolve workspace for project %s: %w", projectID, err)
	}

	dispatch := func(c context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
		return dispatchVerb(c, name, req, configArg, userArg)
	}
	return runClearVia(ctx, out, in, dispatch, kind, projectID, wsID, dryrun, force, stdinIsInteractive(in))
}

// runClearVia is the config-free core: it enumerates and (outside --dryrun,
// after confirmation) tombstones each row through the injected dispatch. Split
// from runClear around the verbDispatch seam so the enumerate → confirm → delete
// flow is unit-testable without a live substrate. `interactive` is whether
// confirmation may be prompted (passed explicitly so tests exercise both the
// TTY and non-TTY paths deterministically).
func runClearVia(ctx context.Context, out io.Writer, in io.Reader, dispatch verbDispatch, kind, projectID, wsID string, dryrun, force, interactive bool) error {
	targets, err := clearTargets(ctx, dispatch, kind, projectID, wsID)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Fprintf(out, "clear: nothing to clear (--kind %s, project %s)\n", kind, projectID)
		return nil
	}

	fmt.Fprintf(out, "clear: %d project row(s) to archive (--kind %s, project %s):\n", len(targets), kind, projectID)
	for _, t := range targets {
		fmt.Fprintf(out, "  - %-40s  %s\n", t.Name, strings.Join(t.Tags, ","))
	}
	if dryrun {
		fmt.Fprintln(out, "dry-run: nothing deleted.")
		return nil
	}

	if !force {
		if !interactive {
			// Clearly non-interactive (a pipe / redirect / non-file reader): the
			// CLI cannot prompt, so refuse rather than silently abort or delete.
			return fmt.Errorf("clear: refusing to archive %d row(s) without confirmation — stdin is not an interactive terminal; re-run with --force", len(targets))
		}
		fmt.Fprintf(out, "archive these %d row(s)? this soft-tombstones them (recoverable) [y/N]: ", len(targets))
		yes, answered := readConfirm(in)
		if !answered {
			// A terminal was detected but no answer arrived (EOF) — the common
			// case for an agent driving the command through its harness, where
			// stdin is a pty with no human at it. Treat the absent answer as a
			// REFUSAL demanding --force, never a silent abort: the operator's
			// real gate here is the harness permission prompt on the command.
			fmt.Fprintln(out)
			return fmt.Errorf("clear: no confirmation received (stdin closed with no answer) — re-run with --force")
		}
		if !yes {
			fmt.Fprintln(out, "aborted — nothing deleted.")
			return nil
		}
	}

	for _, t := range targets {
		status, derr := clearDelete(ctx, dispatch, projectID, wsID, t.Name)
		if derr != nil {
			return fmt.Errorf("clear: delete %q: %w", t.Name, derr)
		}
		fmt.Fprintf(out, "  ✓ %-40s  %s\n", t.Name, status)
	}
	fmt.Fprintf(out, "cleared %d row(s). Recoverable: each is a tombstone (empty-body version); local .satellites/ sources are untouched. Run `satellites skill sync` to refresh.\n", len(targets))
	return nil
}

// clearTargets enumerates the PROJECT-scoped rows the --kind selector covers.
// It lists only the project scope (never system), and defensively drops any row
// whose type is `story` so a hard-deletable story can never enter the kill-list.
func clearTargets(ctx context.Context, dispatch verbDispatch, kind, projectID, wsID string) ([]nounListItem, error) {
	var picked []nounListItem
	seen := map[string]bool{}
	addAll := func(items []nounListItem, keep func(nounListItem) bool) {
		for _, it := range items {
			if it.Type == "story" { // never touch stories — they hard-delete
				continue
			}
			if !keep(it) || seen[it.Name] {
				continue
			}
			seen[it.Name] = true
			picked = append(picked, it)
		}
	}

	if kind == "principle" || kind == "all" {
		// Principles are tag-addressed (no type filter); narrow client-side to
		// the principles:* prefix, exactly as `principle list` does.
		docs, err := listProjectScoped(ctx, dispatch, "", projectID, wsID)
		if err != nil {
			return nil, err
		}
		addAll(docs, func(it nounListItem) bool { return hasTagWithPrefix(it.Tags, "principles:") })
	}
	if kind == "skill" || kind == "workflow" || kind == "all" {
		skills, err := listProjectScoped(ctx, dispatch, "skill", projectID, wsID)
		if err != nil {
			return nil, err
		}
		keep := func(nounListItem) bool { return true }
		if kind == "workflow" {
			keep = func(it nounListItem) bool { return hasTagWithPrefix(it.Tags, "kind:workflow") }
		}
		addAll(skills, keep)
	}

	sort.Slice(picked, func(i, j int) bool { return picked[i].Name < picked[j].Name })
	return picked, nil
}

// listProjectScoped lists the project scope for one type filter (empty lists
// every type, narrowed by the caller). It mirrors the document_list shell the
// substrate-noun listing uses, but pinned to the project scope so system rows
// never enter.
func listProjectScoped(ctx context.Context, dispatch verbDispatch, filterType, projectID, wsID string) ([]nounListItem, error) {
	req := docListRequest{Type: filterType, Scope: "project", WorkspaceID: wsID, ProjectID: projectID, Limit: 200}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := dispatch(ctx, "document_list", raw)
	if err != nil {
		return nil, err
	}
	var parsed docListView
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return nil, fmt.Errorf("decode document_list: %w", err)
	}
	return parsed.Items, nil
}

// clearDelete soft-tombstones one project row by (scope, name) and returns the
// reported tombstone status.
func clearDelete(ctx context.Context, dispatch verbDispatch, projectID, wsID, name string) (string, error) {
	req := map[string]string{"scope": "project", "project_id": projectID, "workspace_id": wsID, "name": name}
	raw, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	resp, err := dispatch(ctx, "document_delete", raw)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Version struct {
			Status  string `json:"status"`
			Version int    `json:"version"`
		} `json:"version"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return "", fmt.Errorf("decode document_delete: %w", err)
	}
	status := parsed.Version.Status
	if status == "" {
		status = "deleted"
	}
	return fmt.Sprintf("%s (v%d)", status, parsed.Version.Version), nil
}

// stdinIsInteractive reports whether the reader is an interactive terminal —
// true only when it is an *os.File backed by a character device. A pipe, a
// regular file, or any non-*os.File reader (an agent's harness, CI, a test
// buffer) is non-interactive, so clear refuses to prompt and demands --force.
// Mirrors the stdin-is-a-TTY test cmd_exec.go uses (no new dependency).
func stdinIsInteractive(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

// readConfirm reads a single answer line. `answered` is false on EOF (no line
// at all) — distinguished from an explicit "n" so the caller can treat an
// absent answer (agent/CI with a pty but no responder) as a refusal demanding
// --force rather than a silent abort. `yes` is true only for y / yes.
func readConfirm(in io.Reader) (yes, answered bool) {
	sc := bufio.NewScanner(in)
	if !sc.Scan() {
		return false, false
	}
	switch strings.TrimSpace(strings.ToLower(sc.Text())) {
	case "y", "yes":
		return true, true
	}
	return false, true
}

func clearKindValid(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}
