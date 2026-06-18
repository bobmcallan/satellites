// `satellites surface check` — the command-surface drift gate
// (sty_d7698c22). A client change that adds, renames, or removes a
// top-level command silently drifts from the reference docs that
// describe the surface; an operator (or agent) then reads stale guidance.
// This gate fires in the client-change loop (named by the commit-push
// routine, the same place the techdebt gate is wired) and fails closed
// when a live command is not documented in the command-surface doc.
//
// Config-over-code (no new MCP verb): the surface doc is read with the
// existing document_get verb, the drift check is the pure surfaceDrift
// core, and the gate is a client-side CLI subcommand — the techdebt-gate
// pattern. The reconcile AC is satisfied by editing the surface doc, not
// by code.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// surfaceDocName is the DEFAULT name of the command-surface reference
// document — a mechanism parameter, not a hard dependency. It is overridable
// per invocation via --doc, and the gate functions even when no such document
// exists: an absent/unreadable doc degrades to "every live command
// undocumented" (actionable drift), never a crash. The gate derives the live
// surface from the binary's own command tree, so its behaviour does not depend
// on any repo-specific document existing (sty_1bdebdd9).
const surfaceDocName = "client-command-surface"

func init() {
	var (
		configArg string
		userArg   string
	)
	root := &cobra.Command{
		Use:   "surface",
		Short: "Command-surface drift gate (keep the reference docs in step with the CLI)",
	}
	root.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	root.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")
	root.AddCommand(newSurfaceCheckCmd(&configArg, &userArg))
	register(root)
}

func newSurfaceCheckCmd(configArg, userArg *string) *cobra.Command {
	var (
		docName     string
		projectID   string
		workspaceID string
	)
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Fail closed when a live CLI command is undocumented in the command-surface doc",
		Long: `check compares the live top-level command surface against the
command-surface reference document (default "` + surfaceDocName + `") and fails
closed when a command exists in the binary but is not named in the doc.

Run it in the client-change loop: when an update adds, renames, or removes a
command, this gate blocks until the reference doc is reconciled to match
(sty_d7698c22). It reads the doc with the existing document_get verb — no new
MCP verb.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			start := time.Now()
			return runSurfaceCheck(ctx, surfaceOpts{
				ConfigPath:  *configArg,
				UserArg:     *userArg,
				DocName:     strings.TrimSpace(docName),
				ProjectID:   strings.TrimSpace(projectID),
				WorkspaceID: strings.TrimSpace(workspaceID),
				Stdout:      cmd.OutOrStdout(),
				Stderr:      cmd.ErrOrStderr(),
				OnVerdict: func(verdict string, blockingFindings int) {
					recordGateVerdict(ctx, *configArg, *userArg, "surface-check", verdict, blockingFindings, time.Since(start), cmd.OutOrStdout())
				},
			})
		},
	}
	cmd.Flags().StringVar(&docName, "doc", surfaceDocName, "Name of the command-surface reference document.")
	cmd.Flags().StringVar(&projectID, "project", "", "project_id of the doc (default: config project_id).")
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "workspace_id of the doc (default: resolved from the project).")
	return cmd
}

type surfaceOpts struct {
	ConfigPath  string
	UserArg     string
	DocName     string
	ProjectID   string
	WorkspaceID string
	Stdout      io.Writer
	Stderr      io.Writer
	// OnVerdict, when set, receives the gate's verdict (CLEAN/BLOCKED) and
	// blocking-finding count at verdict time — the gate-verdict evidence
	// seam (epic:graduated-workflow).
	OnVerdict func(verdict string, blockingFindings int)
}

func runSurfaceCheck(ctx context.Context, opts surfaceOpts) error {
	if opts.DocName == "" {
		opts.DocName = surfaceDocName
	}
	live := liveCommandNames(NewRootCmd())

	body, present, err := loadSurfaceDoc(ctx, opts)
	if err != nil {
		return fmt.Errorf("surface gate: read command-surface doc %q: %w", opts.DocName, err)
	}
	return evaluateSurface(opts.Stdout, live, body, present, opts.DocName, opts.OnVerdict)
}

// evaluateSurface compares the live command surface against the doc body and
// renders the verdict. It is the pure, network-free core of the gate: an
// ABSENT doc (present=false, empty body) degrades to "every live command
// undocumented" — actionable drift, never a crash — so the gate stays atomic
// and self-contained. The referenced doc may enrich the verdict (mark commands
// documented); it can never disable the gate (sty_1bdebdd9).
func evaluateSurface(w io.Writer, live []string, body string, present bool, docName string, onVerdict func(verdict string, blockingFindings int)) error {
	missing := surfaceDrift(live, body)
	printSurfaceReport(w, live, missing, docName, present)
	if onVerdict != nil {
		if len(missing) > 0 {
			onVerdict(gateVerdictBlocked, len(missing))
		} else {
			onVerdict(gateVerdictClean, 0)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("surface gate: %d command(s) undocumented in %q — reconcile the doc to match the CLI", len(missing), docName)
	}
	return nil
}

// liveCommandNames returns the user-facing top-level command names,
// excluding cobra's auto-generated help/completion commands and hidden
// commands (none of which belong in the surface doc).
func liveCommandNames(root *cobra.Command) []string {
	var names []string
	for _, c := range root.Commands() {
		if c.Hidden || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		switch c.Name() {
		case "help", "completion":
			continue
		}
		names = append(names, c.Name())
	}
	sort.Strings(names)
	return names
}

// surfaceDrift returns the live command names that are not named anywhere
// in the doc body, as a whole word. Matching is conservative: a command
// still mentioned in prose is treated as documented (no false block),
// while a newly added/renamed command absent from the doc is flagged —
// the drift that a client update introduces.
func surfaceDrift(live []string, body string) []string {
	var missing []string
	for _, name := range live {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
		if !re.MatchString(body) {
			missing = append(missing, name)
		}
	}
	return missing
}

func printSurfaceReport(w io.Writer, live, missing []string, docName string, docPresent bool) {
	fmt.Fprintln(w, "\n── command-surface review ──")
	fmt.Fprintf(w, "live commands (%d): %s\n", len(live), strings.Join(live, ", "))
	if !docPresent {
		fmt.Fprintf(w, "NOTE: command-surface doc %q is absent or empty — every live command is reported as undocumented; create the doc to track the surface.\n", docName)
	}
	if len(missing) > 0 {
		fmt.Fprintf(w, "UNDOCUMENTED (%d) — add to %s:\n", len(missing), docName)
		for _, c := range missing {
			fmt.Fprintf(w, "  ✗ %s\n", c)
		}
		fmt.Fprintln(w, "verdict: BLOCKED — reconcile the command-surface doc with the CLI.")
		return
	}
	fmt.Fprintln(w, "verdict: CLEAN — every live command is documented. Commit may proceed.")
}

// loadSurfaceDoc fetches the command-surface document body, resolving
// workspace_id from the project when not supplied — the techdebt-gate
// register-load pattern.
//
// It returns present=false (with no error) when the document is ABSENT or
// empty: a missing command-surface doc is not a gate failure, it means the
// surface is undocumented, which evaluateSurface renders as drift. A genuine
// pre-flight failure (no project_id, workspace resolution, transport) is still
// returned as an error — only the no-doc CRASH path is removed (sty_1bdebdd9).
func loadSurfaceDoc(ctx context.Context, opts surfaceOpts) (body string, present bool, err error) {
	projectID := opts.ProjectID
	if projectID == "" {
		if cfg, _, cerr := cliconfig.Load(opts.ConfigPath); cerr == nil {
			projectID = strings.TrimSpace(cfg.ProjectID)
		}
	}
	if projectID == "" {
		return "", false, fmt.Errorf("no project_id (set --project or configure satellites.toml)")
	}
	workspaceID := opts.WorkspaceID
	if workspaceID == "" {
		ws, werr := resolveWorkspaceID(ctx, projectID, opts.ConfigPath, opts.UserArg)
		if werr != nil {
			return "", false, fmt.Errorf("resolve workspace_id: %w", werr)
		}
		workspaceID = ws
	}

	req, err := json.Marshal(verb.DocumentGetRequest{
		Scope:       "project",
		Name:        opts.DocName,
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Inherit:     true,
	})
	if err != nil {
		return "", false, err
	}
	raw, err := dispatchVerb(ctx, "document_get", req, opts.ConfigPath, opts.UserArg)
	if err != nil {
		if isDocNotFound(err) {
			return "", false, nil // doc absent — degrade to drift, do not crash the gate
		}
		return "", false, err
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", false, err
	}
	body = resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[0].Body
	}
	if strings.TrimSpace(body) == "" {
		return "", false, nil // empty doc — same as absent: nothing documented
	}
	return body, true, nil
}

// isDocNotFound reports whether err is a document-not-found from document_get,
// across both the in-process path (verb.ErrNotFound) and the HTTP path (the
// server's "not found" error string, which errors.Is cannot see through the
// transport boundary).
func isDocNotFound(err error) bool {
	return errors.Is(err, verb.ErrNotFound) ||
		strings.Contains(strings.ToLower(err.Error()), "not found")
}

// resolveWorkspaceID maps a project to its workspace via project_get. Shared
// mechanism used by the surface and semantic-search commands; relocated here
// when the techdebt gate-command was retired to config (sty_16f85607).
func resolveWorkspaceID(ctx context.Context, projectID, configPath, userArg string) (string, error) {
	req, err := json.Marshal(verb.ProjectGetRequest{ID: projectID})
	if err != nil {
		return "", err
	}
	raw, err := dispatchVerb(ctx, "project_get", req, configPath, userArg)
	if err != nil {
		return "", err
	}
	var resp struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.WorkspaceID) == "" {
		return "", fmt.Errorf("project %s returned no workspace_id", projectID)
	}
	return resp.WorkspaceID, nil
}
