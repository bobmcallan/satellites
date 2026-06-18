// `satellites hook stopcheck` — the GOAL-KEEPER Stop hook (epic:satellites-backbone
// 1.2, supersedes the sty_b713f886 advisory). Engaging a story sets the goal
// "drive it to a terminal state"; this hook holds the agent to it. While a
// session has a live, non-terminal engagement AND the goal is reachable (a
// governing workflow resolves in the repo), it BLOCKS the stop (exit 2) and
// re-injects the goal, so the agent loops back instead of quietly stopping —
// the satellites equivalent of Claude Code's built-in `/goal`.
//
// It RELEASES the stop (exit 0) when: the story is terminal; there is no live
// engagement; or the goal is UNREACHABLE (no governing workflow resolves) — in
// which case it does NOT loop but prints a report-to-operator note (the
// deadlock escape, so an ungoverned story can never trap the agent). Commits
// left ungated stay as a louder sub-message. Reads the per-repo engagement store
// only — no server round-trip — and any infra hiccup RELEASES the stop (fail
// open: a Stop hook must never trap the agent on an error).

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/workstate"
	"github.com/spf13/cobra"
)

// stopInput is the slice of the Claude Code Stop event the advisory reads.
type stopInput struct {
	SessionID       string `json:"session_id"`
	ParentSessionID string `json:"parent_session_id"`
	Cwd             string `json:"cwd"`
}

// newStopCheckCmd builds the Stop advisory handler.
func newStopCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stopcheck",
		Short: "Goal-keeper Stop hook — block the stop while the engaged story is non-terminal (reachable goal)",
		Long: `stopcheck is the Stop handler and goal-keeper. Engaging a story sets the
goal "drive it to done"; while a live engagement is non-terminal and the goal is
reachable (a governing workflow resolves), it BLOCKS the stop and re-injects the
goal so the agent keeps going. It releases when the story is terminal, when there
is no engagement, or when the goal is unreachable (no governing workflow — it
reports instead of looping). Fails open: any infra hiccup releases the stop.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runHookStopCheck(cmd.InOrStdin(), cmd.ErrOrStderr()) {
				os.Exit(2) // block the stop — the agent loops back to the unmet goal
			}
			return nil
		},
	}
}

// runHookStopCheck is the goal-keeper. It returns true to BLOCK the stop — when
// the session holds a live, non-terminal engagement whose goal is reachable (a
// governing workflow resolves) — after writing the re-injected goal to out
// (stderr, fed back to the agent on exit 2). It returns false to RELEASE on:
// unconfigured repo, no session, store/infra hiccup (fail open), no live
// engagement, a terminal story, or an UNREACHABLE goal (no governing workflow —
// it prints a report note instead of looping). Never errors.
func runHookStopCheck(in io.Reader, out io.Writer) (block bool) {
	raw, _ := io.ReadAll(in)
	var ev stopInput
	_ = json.Unmarshal(raw, &ev) // tolerate empty/garbage → release

	start := strings.TrimSpace(ev.Cwd)
	if start == "" {
		if wd, err := os.Getwd(); err == nil {
			start = wd
		}
	}
	root, ok := findSatellitesRepoRoot(start)
	if !ok {
		return false // unconfigured repo — nothing to keep
	}
	session := sessionKey(ev.SessionID, ev.ParentSessionID)
	if strings.TrimSpace(session) == "" {
		return false
	}
	store, err := workstate.Open(stateDBForRoot(root))
	if err != nil {
		return false // store hiccup must never trap a stop (fail open)
	}
	defer store.Close()
	engs, err := store.LiveEngagement(session)
	if err != nil {
		return false
	}
	cfg := filepath.Join(root, ".satellites", "satellites.toml")
	governed := len(governingWorkflowSources(cfg)) > 0
	now := time.Now().UTC()
	for _, e := range engs {
		// Engine-derived terminal check (no hardcoded names): a KNOWN terminal
		// phase releases the stop. Unresolvable → term=false → the governed/
		// deadlock logic below decides (never traps the agent).
		term, _ := phaseIsTerminal(cfg, e.Phase)
		if e.Phase == phaseCandidate || !e.IsLeaseFresh(now) || term {
			continue
		}
		b, msg := stopGoalDecision(e.Story, e.Phase, commitsSince(root, e.UpdatedAt), governed)
		fmt.Fprintln(out, msg)
		if b {
			block = true
		}
	}
	return block
}

// stopGoalDecision decides, for one live non-terminal engagement, whether to
// block the stop and the message to emit. Pure for tests. A reachable goal
// (governed) blocks with the goal re-injected; commits left ungated add a louder
// sub-message. An UNREACHABLE goal (no governing workflow) does NOT block — it
// returns the report-to-operator note so an ungoverned story cannot trap the
// agent.
func stopGoalDecision(story, phase string, commits int, governed bool) (bool, string) {
	if !governed {
		return false, fmt.Sprintf("satellites: story %s is engaged but %q (not done) and NO governing workflow resolves in this repo — the goal is unreachable. Report the deadlock to the operator (or run `satellites init` to scaffold the gateless baseline). Not blocking.", story, phaseOrUnset(phase))
	}
	msg := fmt.Sprintf("satellites: GOAL NOT MET — story %s is %q, not done. You engaged it to drive it to done: advance it to a terminal state (satellites story status_transition through its gates, or set-status along the gateless baseline), or `satellites work close %s --force` to deliberately abandon it. Do not stop with the goal unmet.", story, phaseOrUnset(phase), story)
	if commits > 0 {
		msg += fmt.Sprintf(" (%d commit(s) were already made this session — shared work left ungated.)", commits)
	}
	return true, msg
}
