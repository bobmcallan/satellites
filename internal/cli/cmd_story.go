// `satellites story run <story-id>` — the operator-observability
// driver (sty_7af47a91). Spawns `claude -p` against a generated
// run_id + session_id, sets the correlation env vars on the
// subprocess, and forwards subprocess stdout/stderr as `log:info` /
// `log:warn` rows into the substrate ledger.
//
// Loop closure: every verb call the subprocess makes back to
// satellites-server carries the same X-Satellites-* headers (via the
// env-to-header bridge in httpclient.go). Server-side middleware
// (sty_0006f5f5) lifts those headers onto request context; arbor's
// LedgerHandler captures every arbor log along the request path as a
// ledger row tagged with run/session/story/project/workspace.
//
// Combined with the per-stream rows this driver writes from the
// outside, the portal /ledger page (sty_becb7019) can render one
// `claude -p` run end-to-end after the fact.

package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

func init() {
	var (
		configArg string
		userArg   string
	)

	story := &cobra.Command{
		Use:   "story",
		Short: "Story commands (drive a story end-to-end via claude -p, …)",
	}
	story.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	story.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")

	story.AddCommand(newStoryRunCmd(&configArg, &userArg))
	story.AddCommand(newStoryReviewCmd(&configArg, &userArg))

	register(story)
}

func newStoryRunCmd(configArg, userArg *string) *cobra.Command {
	var (
		claudeBin   string
		extraPrompt string
	)
	cmd := &cobra.Command{
		Use:   "run <story-id>",
		Short: "Run a story end-to-end via `claude -p`; pipe subprocess output into the ledger",
		Long: `run drives one story through the spawned ` + "`" + `claude -p` + "`" + ` subprocess
and captures every verb-call and stream line into the substrate
ledger under a single run_id. Use the portal /ledger page or the
ledger_list verb to inspect the run after it terminates.

The driver:
  - Resolves the story via document_get and validates project membership.
  - Generates a fresh run_id of shape run_<8hex> and a session_id.
  - Sets SATELLITES_{RUN,SESSION,STORY,PROJECT,WORKSPACE}_ID on the
    subprocess env. satellites-client picks these up and adds matching
    X-Satellites-* headers to every outbound HTTP call; satellites-
    server stamps them onto request context, so arbor's LedgerHandler
    tags every log row along each request with this run.
  - Pipes the claude subprocess's stdout/stderr line-by-line into
    log:info / log:warn ledger rows so the prompt-side trail also
    lands in the audit log.

Inherits the operator's environment unmodified beyond adding the
SATELLITES_* correlation vars. Worktree management, parallel runs,
and resumption are out of scope (sty_7af47a91 out-of-scope list).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runStory(ctx, runStoryOpts{
				StoryID:     strings.TrimSpace(args[0]),
				ConfigPath:  *configArg,
				UserArg:     *userArg,
				ClaudeBin:   claudeBin,
				ExtraPrompt: extraPrompt,
				Stdout:      cmd.OutOrStdout(),
				Stderr:      cmd.ErrOrStderr(),
				Spawn:       defaultSpawn,
			})
		},
	}
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary (defaults to $SATELLITES_CLAUDE_BIN or `claude` on PATH).")
	cmd.Flags().StringVar(&extraPrompt, "extra-prompt", "", "Additional instructions appended to the rendered prompt body.")
	return cmd
}

type runStoryOpts struct {
	StoryID     string
	ConfigPath  string
	UserArg     string
	ClaudeBin   string
	ExtraPrompt string
	Stdout      io.Writer
	Stderr      io.Writer
	Spawn       spawnFunc
}

// spawnFunc is the seam tests use to inject a stub claude binary.
// Production wires `defaultSpawn` (exec.CommandContext over the
// resolved binary). Tests pass a callable that satisfies the same
// shape with deterministic output.
type spawnFunc func(ctx context.Context, binary string, args, env []string) (*exec.Cmd, error)

func defaultSpawn(ctx context.Context, binary string, args, env []string) (*exec.Cmd, error) {
	c := exec.CommandContext(ctx, binary, args...)
	c.Env = env
	return c, nil
}

func runStory(ctx context.Context, opts runStoryOpts) error {
	if opts.StoryID == "" {
		return fmt.Errorf("story id required")
	}
	cfg, _, err := cliconfig.Load(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.IsConfigured() {
		return fmt.Errorf("story run requires a configured satellites.toml — run satellites-init first")
	}

	runID := mintCorrelationID("run")
	sessionID := mintCorrelationID("sess")

	getReq, err := json.Marshal(verb.DocumentGetRequest{ID: opts.StoryID})
	if err != nil {
		return err
	}
	rawDoc, err := dispatchVerb(ctx, "document_get", getReq, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return fmt.Errorf("resolve story: %w", err)
	}
	story, err := decodeStoryEnvelope(rawDoc)
	if err != nil {
		return fmt.Errorf("decode story: %w", err)
	}
	if story.Type != "story" {
		return fmt.Errorf("document %s is type=%q; story run requires type=\"story\"", opts.StoryID, story.Type)
	}
	if cfg.ProjectID != "" && story.ProjectID != cfg.ProjectID {
		return fmt.Errorf("story %s belongs to project %s; satellites.toml is configured for %s",
			story.ID, story.ProjectID, cfg.ProjectID)
	}

	binary, err := resolveClaudeBinary(opts.ClaudeBin)
	if err != nil {
		return err
	}
	prompt := renderRunPrompt(story, opts.ExtraPrompt)
	claudeArgs := []string{
		"--permission-mode", "bypassPermissions",
		"--strict-mcp-config",
		"--output-format", "stream-json",
		"--verbose",
		"-p", prompt,
	}
	env := append(os.Environ(),
		"SATELLITES_RUN_ID="+runID,
		"SATELLITES_SESSION_ID="+sessionID,
		"SATELLITES_STORY_ID="+story.ID,
		"SATELLITES_PROJECT_ID="+story.ProjectID,
		"SATELLITES_WORKSPACE_ID="+story.WorkspaceID,
	)

	// Stamp the driver process too — every verb call it issues (the
	// per-line ledger_append POSTs below) carries the same headers.
	_ = os.Setenv("SATELLITES_RUN_ID", runID)
	_ = os.Setenv("SATELLITES_SESSION_ID", sessionID)
	_ = os.Setenv("SATELLITES_STORY_ID", story.ID)
	_ = os.Setenv("SATELLITES_PROJECT_ID", story.ProjectID)
	_ = os.Setenv("SATELLITES_WORKSPACE_ID", story.WorkspaceID)

	tags := correlationTags{
		RunID:       runID,
		SessionID:   sessionID,
		StoryID:     story.ID,
		ProjectID:   story.ProjectID,
		WorkspaceID: story.WorkspaceID,
	}
	fmt.Fprintf(opts.Stderr, "satellites story run: story=%s run=%s session=%s\n",
		story.ID, runID, sessionID)
	if err := appendLedgerLine(ctx, opts.ConfigPath, opts.UserArg, tags, "log:info",
		"claude:start", map[string]any{"prompt_bytes": len(prompt), "binary": binary}); err != nil {
		fmt.Fprintf(opts.Stderr, "ledger append claude:start failed: %v\n", err)
	}

	cmdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	subprocess, err := opts.Spawn(cmdCtx, binary, claudeArgs, env)
	if err != nil {
		return fmt.Errorf("spawn claude: %w", err)
	}

	// cmd.Stdout / cmd.Stderr → io.MultiWriter(tee, lineForwarder).
	// The lineForwarder splits the byte stream into lines and invokes
	// appendLedgerLine for each. exec.Cmd.Wait blocks until every byte
	// the subprocess wrote has been delivered to these writers, so
	// there's no race window between subprocess exit and forwarder
	// completion. Using StdoutPipe + a goroutine reader instead races
	// against Wait's pipe-close and intermittently drops the final
	// lines under test load (sty_7af47a91 dev: TestRunStory was flaky
	// in `go test ./...` for exactly this reason).
	stdoutForwarder := newLineForwarder(func(line string) {
		if err := appendLedgerLine(ctx, opts.ConfigPath, opts.UserArg, tags,
			"log:info", "claude:stream", map[string]any{"line": line}); err != nil {
			fmt.Fprintf(opts.Stderr, "ledger append claude:stream failed: %v\n", err)
		}
	})
	stderrForwarder := newLineForwarder(func(line string) {
		if err := appendLedgerLine(ctx, opts.ConfigPath, opts.UserArg, tags,
			"log:warn", "claude:stderr", map[string]any{"line": line}); err != nil {
			fmt.Fprintf(opts.Stderr, "ledger append claude:stderr failed: %v\n", err)
		}
	})
	if opts.Stdout != nil {
		subprocess.Stdout = io.MultiWriter(opts.Stdout, stdoutForwarder)
	} else {
		subprocess.Stdout = stdoutForwarder
	}
	if opts.Stderr != nil {
		subprocess.Stderr = io.MultiWriter(opts.Stderr, stderrForwarder)
	} else {
		subprocess.Stderr = stderrForwarder
	}

	waitErr := subprocess.Run()
	stdoutForwarder.Flush()
	stderrForwarder.Flush()

	exitCode := 0
	if subprocess.ProcessState != nil {
		exitCode = subprocess.ProcessState.ExitCode()
	}
	if err := appendLedgerLine(ctx, opts.ConfigPath, opts.UserArg, tags, "log:info",
		"claude:exit", map[string]any{"exit_code": exitCode}); err != nil {
		fmt.Fprintf(opts.Stderr, "ledger append claude:exit failed: %v\n", err)
	}
	if waitErr != nil {
		// Non-zero exit isn't a CLI-side error — surface the code and
		// return nil so the operator gets a clean shell exit they can
		// branch on themselves. Stderr already carries the detail.
		fmt.Fprintf(opts.Stderr, "claude exited with code %d\n", exitCode)
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
	return nil
}

// lineForwarder is an io.Writer that splits its input on newline
// boundaries and invokes cb for each complete line (without the
// trailing newline). Flush emits any trailing bytes that arrived
// without a terminating newline so the final partial line is still
// captured. Thread-safe — exec.Cmd may dispatch multiple Write calls
// from internal goroutines.
type lineForwarder struct {
	mu  sync.Mutex
	buf bytes.Buffer
	cb  func(string)
}

func newLineForwarder(cb func(string)) *lineForwarder {
	return &lineForwarder{cb: cb}
}

func (f *lineForwarder) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buf.Write(p)
	for {
		data := f.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		line := string(data[:idx])
		f.buf.Next(idx + 1)
		f.cb(line)
	}
	return len(p), nil
}

func (f *lineForwarder) Flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buf.Len() == 0 {
		return
	}
	f.cb(strings.TrimRight(f.buf.String(), "\n"))
	f.buf.Reset()
}

type correlationTags struct {
	RunID, SessionID, StoryID, ProjectID, WorkspaceID string
}

func appendLedgerLine(
	ctx context.Context,
	configPath, userArg string,
	tags correlationTags,
	kind, body string,
	payload map[string]any,
) error {
	var payloadBytes json.RawMessage
	if len(payload) > 0 {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		payloadBytes = b
	}
	req := verb.LedgerAppendRequest{
		StoryID:     tags.StoryID,
		ProjectID:   tags.ProjectID,
		WorkspaceID: tags.WorkspaceID,
		SessionID:   tags.SessionID,
		RunID:       tags.RunID,
		Kind:        kind,
		Body:        body,
		Payload:     payloadBytes,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := dispatchVerb(ctx, "ledger_append", raw, configPath, userArg); err != nil {
		return err
	}
	return nil
}

// storyEnvelope is the minimal projection the driver needs from a
// document_get response — typed inline so we don't touch the
// internal/document or internal/ledger types from the CLI layer.
type storyEnvelope struct {
	ID          string
	Type        string
	ProjectID   string
	WorkspaceID string
	Name        string
}

func decodeStoryEnvelope(raw json.RawMessage) (storyEnvelope, error) {
	var wrap struct {
		Document storyEnvelope `json:"document"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return storyEnvelope{}, err
	}
	return wrap.Document, nil
}

// storyEnvelope implements json.Unmarshaler so the field names line
// up with the document_get response's snake_case keys.
func (s *storyEnvelope) UnmarshalJSON(b []byte) error {
	var aux struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		ProjectID   string `json:"project_id"`
		WorkspaceID string `json:"workspace_id"`
		Name        string `json:"name"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	s.ID = aux.ID
	s.Type = aux.Type
	s.ProjectID = aux.ProjectID
	s.WorkspaceID = aux.WorkspaceID
	s.Name = aux.Name
	return nil
}

func mintCorrelationID(prefix string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

func resolveClaudeBinary(override string) (string, error) {
	if s := strings.TrimSpace(override); s != "" {
		return s, nil
	}
	if s := strings.TrimSpace(os.Getenv("SATELLITES_CLAUDE_BIN")); s != "" {
		return s, nil
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("claude binary not found on PATH (set --claude-bin or SATELLITES_CLAUDE_BIN): %w", err)
	}
	return filepath.Clean(bin), nil
}

// renderRunPrompt produces the short prompt body the dispatched
// claude subprocess receives. Per sty_7af47a91 AC7 it names the
// story / project / workspace ids and points the agent at substrate
// verbs rather than pasting story content — the agent reads the body
// itself via document_get.
func renderRunPrompt(s storyEnvelope, extra string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are dispatched to drive satellites story %s.\n\n", s.ID)
	fmt.Fprintf(&b, "Story:     %s\nProject:   %s\nWorkspace: %s\n\n", s.ID, s.ProjectID, s.WorkspaceID)
	b.WriteString("Bootstrap via the satellites CLI (auto-authenticated against the operator's TOML):\n")
	b.WriteString("  satellites exec document_get --json '{\"id\":\"" + s.ID + "\"}'   — full story body + acceptance criteria.\n")
	b.WriteString("  satellites document list --scope project ...                        — sibling docs in the project.\n")
	b.WriteString("  satellites principle list --scope project ...                       — principles in force.\n\n")
	b.WriteString("Drive the story through its workflow gate (client-side — the gate\n")
	b.WriteString("reads the workflow skill from THIS worktree, which the server cannot see):\n")
	b.WriteString("  satellites story review " + s.ID + "\n\n")
	b.WriteString("Every verb call you make from this subprocess carries the run's\n")
	b.WriteString("correlation headers; the portal /ledger page will show your trail.\n")
	if e := strings.TrimSpace(extra); e != "" {
		b.WriteString("\nOperator instructions:\n")
		b.WriteString(e)
		b.WriteString("\n")
	}
	return b.String()
}
