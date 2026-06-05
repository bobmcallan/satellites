// `satellites hook access` (PreToolUse on the satellites MCP story-fetch) and
// `satellites hook prompt` (UserPromptSubmit) — the story-ACCESS trigger
// (sty_79af820a, epic:process-adherence). Unlike the START door, these are
// ADVISORY: they never block. When the agent fetches a story (or names one in a
// prompt) for which this session has no live engagement, they record a candidate
// engagement in the per-repo event store and inject a reminder to
// `satellites work init <story>`. Accessing a story is not the same as working
// on it, so a reminder — not a deny — is the right response; the hard gate is the
// edit door. Phase-agnostic: the trigger is "a story was pulled", independent of
// whatever phases the user's workflow defines. Fails open (advisory): a
// missing/unconfigured store yields no reminder and never blocks.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/workstate"
	"github.com/spf13/cobra"
)

// phaseCandidate marks a current row created by a story ACCESS (read) as opposed
// to an engaged story. It is deliberately NOT in any workflow's editable set, so
// a candidate never authorises edits at the door.
const phaseCandidate = "candidate"

// storyIDRe matches a satellites story id wherever it appears (a tool_input id
// or free-form prompt text).
var storyIDRe = regexp.MustCompile(`sty_[0-9a-f]+`)

// newAccessCmd builds the PreToolUse story-fetch handler.
func newAccessCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "access",
		Short: "PreToolUse story-access trigger — advisory engage reminder on a story fetch",
		Long: `access is the PreToolUse handler matched to the satellites MCP story-fetch
(mcp__satellites__document_get). It reads the event JSON on stdin; when the
fetched story has no live engagement for this session, it records a candidate
engagement and injects an advisory reminder to run satellites work init. It
NEVER blocks — accessing a story is not working on it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookAccess(cmd.InOrStdin(), cmd.OutOrStdout(), time.Now().UTC())
		},
	}
}

// newPromptCmd builds the UserPromptSubmit handler.
func newPromptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prompt",
		Short: "UserPromptSubmit story-access trigger — advisory reminder when a sty_ appears in the prompt",
		Long: `prompt is the UserPromptSubmit handler. It scans the submitted prompt for a
story id; when one is present with no live engagement for this session, it
injects the same advisory engage reminder. Best-effort (natural language); it
never blocks the prompt.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookPrompt(cmd.InOrStdin(), cmd.OutOrStdout(), time.Now().UTC())
		},
	}
}

// accessInput is the slice of the PreToolUse event the access trigger reads.
type accessInput struct {
	SessionID string          `json:"session_id"`
	Cwd       string          `json:"cwd"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// promptInput is the slice of the UserPromptSubmit event the prompt trigger reads.
type promptInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Prompt    string `json:"prompt"`
}

func runHookAccess(in io.Reader, out io.Writer, now time.Time) error {
	raw, _ := io.ReadAll(in)
	var ev accessInput
	_ = json.Unmarshal(raw, &ev) // tolerate empty/garbage
	story := storyIDFromToolInput(ev.ToolInput)
	if story == "" {
		return nil // not a story fetch → silent allow
	}
	if remind, reminder := assessAccess(ev.Cwd, ev.SessionID, story, now); remind {
		return emitAdditionalContext(out, "PreToolUse", reminder)
	}
	return nil
}

func runHookPrompt(in io.Reader, out io.Writer, now time.Time) error {
	raw, _ := io.ReadAll(in)
	var ev promptInput
	_ = json.Unmarshal(raw, &ev)
	story := storyIDRe.FindString(ev.Prompt)
	if story == "" {
		return nil
	}
	if remind, reminder := assessAccess(ev.Cwd, ev.SessionID, story, now); remind {
		return emitAdditionalContext(out, "UserPromptSubmit", reminder)
	}
	return nil
}

// assessAccess records the access in the per-repo store and decides whether to
// remind. It NEVER blocks: a missing/unconfigured store or any error returns no
// reminder (fail-open) — the edit door, not this trigger, is the hard gate. The
// session×story key means concurrent agents and sequential stories are tracked
// independently; the fast path is a single indexed read of `current`.
func assessAccess(cwd, session, story string, now time.Time) (remind bool, reminder string) {
	start := strings.TrimSpace(cwd)
	if start == "" {
		if wd, err := os.Getwd(); err == nil {
			start = wd
		}
	}
	root, ok := findSatellitesRepoRoot(start)
	if !ok {
		return false, "" // unconfigured repo — advisory does nothing
	}
	if strings.TrimSpace(session) == "" {
		session = "unknown"
	}
	store, err := workstate.Open(stateDBForRoot(root))
	if err != nil {
		return false, ""
	}
	defer store.Close()

	if eng, ok, err := store.Current(session, story); err == nil && ok && isLiveEngagement(eng) {
		return false, "" // already engaged → silent
	}
	// Record a candidate engagement (observe) and remind.
	_, _ = store.Append(workstate.Event{Session: session, Story: story, Phase: phaseCandidate, Kind: "candidate", TS: now})
	return true, fmt.Sprintf(
		"satellites: you accessed story %s but this session has no active engagement for it. "+
			"If you intend to WORK on it, run `satellites work init %s` first to engage its workflow. "+
			"If you are only reading, ignore this.", story, story)
}

// isLiveEngagement reports whether a current row is an engaged story rather than
// a bare candidate from a prior access. The richer lease/editable-phase
// semantics arrive with the engagement-lifecycle story (sty_8852d23e); here a
// non-candidate, non-empty phase counts as engaged.
func isLiveEngagement(e workstate.Engagement) bool {
	p := strings.TrimSpace(e.Phase)
	return p != "" && p != phaseCandidate
}

// storyIDFromToolInput pulls a story id out of a tool_input payload: the `id`
// field when it is a story id, else a scan of the raw input (covers story:<id>
// forms). Returns "" when no story id is present (a non-story fetch).
func storyIDFromToolInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var ti struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &ti) == nil {
		if id := strings.TrimSpace(ti.ID); strings.HasPrefix(id, "sty_") {
			return storyIDRe.FindString(id)
		}
	}
	return storyIDRe.FindString(string(raw))
}

// stateDBForRoot resolves the engagement store path for a known repo root,
// mirroring gateWorkDir: config-driven, default when the toml can't be loaded.
func stateDBForRoot(root string) string {
	cfg, _, err := cliconfig.Load(filepath.Join(root, ".satellites", "satellites.toml"))
	if err != nil {
		return filepath.Join(root, cliconfig.DefaultStateDB)
	}
	return cfg.ResolveStateDB(root)
}

// hookContextOut is the Claude Code hook output that injects advisory context
// without a permission decision — so the tool/prompt proceeds normally.
type hookContextOut struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func emitAdditionalContext(out io.Writer, event, context string) error {
	var doc hookContextOut
	doc.HookSpecificOutput.HookEventName = event
	doc.HookSpecificOutput.AdditionalContext = context
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(b))
	return nil
}
