// `satellites hook stopcheck` — the Stop advisory (epic:enforcement-surface,
// sty_b713f886). At the end of a turn it checks whether the session committed
// work while its engaged story is still non-terminal, and emits a loud advisory
// pointing at the ungated work. It is the backstop to the commit-gate + the
// work-close guard: it cannot un-push, so it NEVER blocks (a Stop hook that
// exits non-zero would block the stop) — it only surfaces the gap.
//
// Silent unless there is something to say: an unconfigured repo, no live
// engagement, a terminal story, or no commits since engagement all produce no
// output. Reads the per-repo engagement store only — no server round-trip.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
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
		Short: "Stop advisory — flag commits made this session while the engaged story stayed non-terminal",
		Long: `stopcheck is the Stop handler. At end of turn it warns when the session
committed work but its engaged story is still non-terminal — shared work that
never reached done. It NEVER blocks the stop (it cannot un-push); it only
surfaces the gap. Silent when there is nothing to flag.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookStopCheck(cmd.InOrStdin(), cmd.ErrOrStderr())
		},
	}
}

// runHookStopCheck emits an advisory (never an error, never a block) for each
// live, non-terminal engagement of the session that has commits since it was
// engaged. Always returns nil so the stop is never blocked.
func runHookStopCheck(in io.Reader, out io.Writer) error {
	raw, _ := io.ReadAll(in)
	var ev stopInput
	_ = json.Unmarshal(raw, &ev) // tolerate empty/garbage → silent

	start := strings.TrimSpace(ev.Cwd)
	if start == "" {
		if wd, err := os.Getwd(); err == nil {
			start = wd
		}
	}
	root, ok := findSatellitesRepoRoot(start)
	if !ok {
		return nil // unconfigured repo — advisory does nothing
	}
	session := sessionKey(ev.SessionID, ev.ParentSessionID)
	if strings.TrimSpace(session) == "" {
		return nil
	}
	store, err := workstate.Open(stateDBForRoot(root))
	if err != nil {
		return nil // store hiccup must never block a stop
	}
	defer store.Close()
	engs, err := store.LiveEngagement(session)
	if err != nil {
		return nil
	}
	now := time.Now().UTC()
	for _, e := range engs {
		if e.Phase == phaseCandidate || !e.IsLeaseFresh(now) || isTerminalPhase(e.Phase) {
			continue
		}
		if n := commitsSince(root, e.UpdatedAt); n > 0 {
			fmt.Fprintf(out, "satellites: %d commit(s) were made this session but story %s is still %q (not done). "+
				"Drive it to done through its gates before ending, or `satellites work close %s --force` to deliberately abandon it — don't leave shared work ungated.\n",
				n, e.Story, phaseOrUnset(e.Phase), e.Story)
		}
	}
	return nil
}
