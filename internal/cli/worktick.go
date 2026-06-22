// Engagement ticker (sty_84c55d0d). A leased session's client activity — any
// CLI command, including the per-edit `hook gate` — is the heartbeat: it
// refreshes the local lease with a throttled `tick` event and hands delivery
// to a DETACHED `work sync` child process. The foreground path performs no
// network I/O, so an unreachable server can neither fail nor slow a call;
// undelivered events wait behind the flush high-water mark for the next tick
// or an explicit `satellites work sync`.

package cli

import (
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/workstate"
)

// engagementTickThrottle bounds tick volume: a session's engagement is
// re-stamped at most once per throttle window, however dense the client
// activity (per-edit hook fires included).
const engagementTickThrottle = 30 * time.Second

// tickCommandSkip names command paths the root ticker must not run for:
// `work sync` is the flush itself (a detached child ticking would self-spawn),
// and the no-op commands carry no engagement signal.
var tickCommandSkip = map[string]bool{
	"work sync":  true,
	"help":       true,
	"version":    true,
	"completion": true,
}

// tickEngagement re-stamps the session's live engagement(s) with a fresh
// lease when the last stamp is at least one throttle window old. Returns
// whether a tick event was recorded. Pure over the store — testable with a
// fixed now; every failure is swallowed by the caller (the ticker is never
// load-bearing for the foreground command).
func tickEngagement(stateDB, session string, now time.Time) (bool, error) {
	store, err := workstate.Open(stateDB)
	if err != nil {
		return false, err
	}
	defer store.Close()
	engs, err := store.LiveEngagement(session)
	if err != nil {
		return false, err
	}
	ticked := false
	for _, e := range engs {
		if e.Phase == phaseCandidate || !e.IsLeaseFresh(now) {
			continue // candidates and expired leases are not "processing"
		}
		if now.Sub(e.UpdatedAt) < engagementTickThrottle {
			continue
		}
		if _, err := store.Append(workstate.Event{
			Session: e.Session, Story: e.Story, Phase: e.Phase,
			Kind: "tick", LeaseUntil: now.Add(engageLeaseTTL), Editable: e.Editable, CommitReady: e.CommitReady, TS: now,
		}); err != nil {
			return ticked, err
		}
		ticked = true
	}
	return ticked, nil
}

// spawnDetachedFlush starts `<self> work sync` as a detached child and does
// not wait: the child delivers the unflushed tail (or fails silently, leaving
// the high-water mark for the next attempt). The foreground command pays only
// the process spawn.
func spawnDetachedFlush(configArg string) {
	self, err := os.Executable()
	if err != nil {
		return
	}
	args := []string{"work", "sync"}
	if strings.TrimSpace(configArg) != "" {
		args = append(args, "--config", configArg)
	}
	cmd := exec.Command(self, args...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = nil, nil, nil
	if cmd.Start() == nil {
		// Detach: the child is reparented on our exit; Release drops our handle.
		_ = cmd.Process.Release()
	}
}

// tickAndFlush is the root-command ticker: throttled local tick, then a
// detached flush when a tick landed (the throttle also bounds child spawns).
// Best-effort end to end — it never returns an error to the foreground path.
func tickAndFlush(configArg string) {
	ticked, err := tickEngagement(resolveStateDB(configArg), resolveSession(""), time.Now().UTC())
	if err != nil || !ticked {
		return
	}
	spawnDetachedFlush(configArg)
}
