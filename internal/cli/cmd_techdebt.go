// `satellites techdebt review` — the technical-debt pre-commit gate
// (sty_dd128ef6, the enforcement half of the broken-windows programme). It runs
// the verification the done-review gate cannot — build + unit + the integration
// tier — parses the failing checks, reconciles them against the quarantine
// register (a substrate document), and fails closed on any unregistered red.
//
// At commit the tree must be CLEAN or its debt must be a STORY: a failing check
// passes only when the register names it AND names the owning story; a fresh
// uncovered red, or a register row with no owner, blocks. Closed windows (a
// registered check that now passes) are reported so the register shrinks.
//
// Config-over-code (no new MCP verb): the register is read with the existing
// document_get verb, the reconcile is the pure internal/techdebt core, and the
// commit-push routine names this command via the satellites-technical-debt-review
// skill. The CLI stays behind internal/verb's request/response types per the
// layering guard (no internal/document import).

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/techdebt"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// defaultRegisterName is the substrate document the gate reconciles against.
const defaultRegisterName = "technical-debt-register"

// dockerHints are substrings that mark an integration run that could not even
// START (Docker/testcontainers unavailable) rather than a test that failed. A
// tier that cannot start is SKIPPED with a warning, not treated as a wall of
// new reds — the operator machine may not have Docker up at every commit.
var dockerHints = []string{
	"cannot connect to the docker daemon",
	"is the docker daemon running",
	"error during connect",
	"testcontainers",
	"[setup failed]",
	"could not start",
}

func init() {
	var (
		configArg string
		userArg   string
	)
	root := &cobra.Command{
		Use:   "techdebt",
		Short: "Technical-debt pre-commit gate (broken-windows enforcement)",
	}
	root.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	root.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")
	root.AddCommand(newTechdebtReviewCmd(&configArg, &userArg))
	register(root)
}

func newTechdebtReviewCmd(configArg, userArg *string) *cobra.Command {
	var (
		worktree        string
		registerName    string
		projectID       string
		workspaceID     string
		integrationPkgs string
		skipIntegration bool
	)
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Run build + unit + integration and fail closed on an unregistered red",
		Long: `review runs the technical-debt gate before a commit.

It runs ` + "`go build ./...`" + `, ` + "`go test ./...`" + ` (unit), and the
integration tier, parses the failing checks, and reconciles them against the
quarantine register (a substrate document, default "` + defaultRegisterName + `").

A failing check passes only when the register names it and names its owning
story; a fresh uncovered red — or a register row with no story — blocks the
commit (exit 1). A registered check that now passes is reported as stale so the
register can shrink. A broken build always blocks (it is never registerable). If
the integration tier cannot start (no Docker), it is skipped with a warning.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			start := time.Now()
			return runTechdebtReview(ctx, techdebtOpts{
				OnVerdict: func(verdict string, blockingFindings int) {
					recordGateVerdict(ctx, *configArg, *userArg, "techdebt-review", verdict, blockingFindings, time.Since(start), cmd.OutOrStdout())
				},
				ConfigPath:      *configArg,
				UserArg:         *userArg,
				Worktree:        worktree,
				RegisterName:    strings.TrimSpace(registerName),
				ProjectID:       strings.TrimSpace(projectID),
				WorkspaceID:     strings.TrimSpace(workspaceID),
				IntegrationPkgs: strings.TrimSpace(integrationPkgs),
				SkipIntegration: skipIntegration,
				Stdout:          cmd.OutOrStdout(),
				Stderr:          cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&worktree, "worktree", "", "Repo root the suite runs in (default: current directory).")
	cmd.Flags().StringVar(&registerName, "register", defaultRegisterName, "Name of the quarantine-register document.")
	cmd.Flags().StringVar(&projectID, "project", "", "project_id of the register (default: config project_id).")
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "workspace_id of the register (default: resolved from the project).")
	cmd.Flags().StringVar(&integrationPkgs, "integration-pkgs", "./tests/integration/...", "Package pattern for the integration tier.")
	cmd.Flags().BoolVar(&skipIntegration, "skip-integration", false, "Skip the integration tier (e.g. when Docker is unavailable).")
	return cmd
}

type techdebtOpts struct {
	ConfigPath      string
	UserArg         string
	Worktree        string
	RegisterName    string
	ProjectID       string
	WorkspaceID     string
	IntegrationPkgs string
	SkipIntegration bool
	Stdout          io.Writer
	Stderr          io.Writer
	// OnVerdict, when set, receives the gate's final verdict (CLEAN/BLOCKED)
	// and its blocking-finding count at verdict time — the seam the
	// gate-verdict evidence recorder hangs off (epic:graduated-workflow).
	OnVerdict func(verdict string, blockingFindings int)
}

// emitVerdict invokes the OnVerdict seam when wired — nil-safe.
func (o techdebtOpts) emitVerdict(verdict string, blockingFindings int) {
	if o.OnVerdict != nil {
		o.OnVerdict(verdict, blockingFindings)
	}
}

func runTechdebtReview(ctx context.Context, opts techdebtOpts) error {
	dir := opts.Worktree
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	if opts.RegisterName == "" {
		opts.RegisterName = defaultRegisterName
	}

	// 1. Build is never registerable — a broken build always blocks.
	if out, code := runGo(ctx, dir, "build", "./..."); code != 0 {
		fmt.Fprintf(opts.Stdout, "BLOCKED: `go build ./...` failed — a broken build is never tolerable debt.\n\n%s\n", strings.TrimSpace(out))
		opts.emitVerdict(gateVerdictBlocked, 1)
		return fmt.Errorf("technical-debt gate: build failed")
	}
	fmt.Fprintln(opts.Stdout, "build: ok")

	var failing []string
	// complete tracks whether every tier actually ran. A skipped tier means we
	// cannot claim a registered check in that tier has gone green — so stale
	// (window-closed) detection is only authoritative on a complete run.
	complete := true

	// 2. Unit tier. A unit-tier failure with no parsed test (a compile/setup
	// failure) is a hard block — it is not a registerable check.
	unitOut, unitCode := runGo(ctx, dir, "test", "./...")
	unitFails := techdebt.ParseFailures(unitOut)
	if unitCode != 0 && len(unitFails) == 0 {
		fmt.Fprintf(opts.Stdout, "BLOCKED: unit tier failed without a named test (build/setup failure).\n\n%s\n", tail(unitOut, 60))
		opts.emitVerdict(gateVerdictBlocked, 1)
		return fmt.Errorf("technical-debt gate: unit tier failed to run")
	}
	failing = append(failing, unitFails...)
	fmt.Fprintf(opts.Stdout, "unit: %d failing test(s)\n", len(unitFails))

	// 3. Integration tier — the tier that runs in no other gate. Skipped (not
	// blocked) when it cannot start (no Docker).
	if opts.SkipIntegration {
		complete = false
		fmt.Fprintln(opts.Stdout, "integration: skipped (--skip-integration)")
	} else {
		// -p 1: run integration test packages serially (sty_0c98760e). The tier
		// is already effectively sequential (no test calls t.Parallel), but this
		// makes the intent explicit and keeps timing/chromedp tests from
		// contending if more integration packages are ever added.
		intOut, intCode := runGo(ctx, dir, "test", "-p", "1", "-tags", "integration", opts.IntegrationPkgs)
		intFails := techdebt.ParseFailures(intOut)
		switch {
		case intCode != 0 && len(intFails) == 0 && looksLikeInfra(intOut):
			complete = false
			fmt.Fprintf(opts.Stderr, "warn: integration tier could not start (no Docker?) — skipped, not blocked.\n")
		case intCode != 0 && len(intFails) == 0:
			fmt.Fprintf(opts.Stdout, "BLOCKED: integration tier failed without a named test (build/setup failure).\n\n%s\n", tail(intOut, 60))
			opts.emitVerdict(gateVerdictBlocked, 1)
			return fmt.Errorf("technical-debt gate: integration tier failed to run")
		default:
			failing = append(failing, intFails...)
			fmt.Fprintf(opts.Stdout, "integration: %d failing test(s)\n", len(intFails))
		}
	}

	// 4. Load + parse the register (substrate document; no new verb).
	register, regErr := loadRegister(ctx, opts)
	if regErr != nil {
		fmt.Fprintf(opts.Stderr, "warn: register %q not read (%v) — treating as empty; every failure counts as a new red.\n", opts.RegisterName, regErr)
	}

	// 5. Reconcile and report. On an incomplete run (a tier was skipped) the
	// stale set is not trustworthy — a registered check that did not run only
	// LOOKS green — so suppress the window-closed recommendation.
	res := techdebt.Reconcile(failing, register)
	if !complete {
		res.Stale = nil
	}
	printTechdebtReport(opts.Stdout, res, complete)

	if !res.OK {
		opts.emitVerdict(gateVerdictBlocked, len(res.NewRed)+len(res.Unowned))
		return fmt.Errorf("technical-debt gate: %d new red, %d unowned register entr(ies) — fix it, or file a story and add an owned register row", len(res.NewRed), len(res.Unowned))
	}
	opts.emitVerdict(gateVerdictClean, 0)
	return nil
}

// loadRegister fetches the register document and parses its rows. It resolves
// workspace_id from the project when not supplied. A missing register is not an
// error to the caller — an empty register means every failure is a new red.
func loadRegister(ctx context.Context, opts techdebtOpts) ([]techdebt.Entry, error) {
	projectID := opts.ProjectID
	if projectID == "" {
		if cfg, _, err := cliconfig.Load(opts.ConfigPath); err == nil {
			projectID = strings.TrimSpace(cfg.ProjectID)
		}
	}
	if projectID == "" {
		return nil, fmt.Errorf("no project_id (set --project or configure satellites.toml)")
	}
	workspaceID := opts.WorkspaceID
	if workspaceID == "" {
		ws, err := resolveWorkspaceID(ctx, projectID, opts.ConfigPath, opts.UserArg)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace_id: %w", err)
		}
		workspaceID = ws
	}

	req, err := json.Marshal(verb.DocumentGetRequest{
		Scope:       "project",
		Name:        opts.RegisterName,
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Inherit:     true,
	})
	if err != nil {
		return nil, err
	}
	raw, err := dispatchVerb(ctx, "document_get", req, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return nil, err
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	body := resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[0].Body
	}
	return techdebt.ParseRegister(body), nil
}

// resolveWorkspaceID looks up the project's workspace_id via project_get. The
// CLI decodes only the one field it needs (layering guard: no internal/project
// import). Shared by the techdebt and surface gates.
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

// runGo runs a `go` subcommand in dir and returns its combined output and exit
// code. A non-zero code is normal (a failing test); the caller classifies it.
func runGo(ctx context.Context, dir string, args ...string) (string, int) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return string(out), code
}

// looksLikeInfra reports whether a tier's output marks an environment that
// could not start the suite (Docker down) rather than a test failure.
func looksLikeInfra(out string) bool {
	low := strings.ToLower(out)
	for _, h := range dockerHints {
		if strings.Contains(low, h) {
			return true
		}
	}
	return false
}

func printTechdebtReport(w io.Writer, res techdebt.Result, complete bool) {
	fmt.Fprintln(w, "\n── technical-debt review ──")
	if !complete {
		fmt.Fprintln(w, "(partial run — a tier was skipped; registered debt in that tier was not re-verified)")
	}
	if len(res.NewRed) > 0 {
		fmt.Fprintf(w, "NEW RED (%d) — fix, or file a story and register as owned debt:\n", len(res.NewRed))
		for _, c := range res.NewRed {
			fmt.Fprintf(w, "  ✗ %s\n", c)
		}
	}
	if len(res.Unowned) > 0 {
		fmt.Fprintf(w, "UNOWNED register entr(ies) (%d) — every window must name its story:\n", len(res.Unowned))
		for _, e := range res.Unowned {
			fmt.Fprintf(w, "  ✗ %s\n", e.CheckID)
		}
	}
	if len(res.Registered) > 0 {
		fmt.Fprintf(w, "registered debt (%d) — owned, tolerated:\n", len(res.Registered))
		for _, e := range res.Registered {
			fmt.Fprintf(w, "  • %s (%s)\n", e.CheckID, e.StoryID)
		}
	}
	if len(res.Stale) > 0 {
		fmt.Fprintf(w, "STALE (%d) — window closed, remove from the register (it only shrinks):\n", len(res.Stale))
		for _, e := range res.Stale {
			fmt.Fprintf(w, "  ↓ %s (%s)\n", e.CheckID, e.StoryID)
		}
	}
	if res.OK {
		fmt.Fprintln(w, "verdict: CLEAN — the tree is clean or its debt is owned. Commit may proceed.")
	} else {
		fmt.Fprintln(w, "verdict: BLOCKED — clean it, or make the debt a story.")
	}
}

// tail returns the last n lines of s (for surfacing a failing build/setup log
// without dumping the whole run).
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
