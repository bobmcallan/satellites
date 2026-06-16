package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestStopGoalDecision: a reachable (governed) unmet goal blocks with the goal
// re-injected; ungated commits add a louder sub-message; an unreachable goal
// (ungoverned) does NOT block and reports the deadlock instead.
func TestStopGoalDecision(t *testing.T) {
	if b, msg := stopGoalDecision("sty_a", "in_progress", 0, true); !b || !strings.Contains(msg, "GOAL NOT MET") {
		t.Errorf("governed unmet goal must block with the goal: b=%v msg=%q", b, msg)
	}
	if b, msg := stopGoalDecision("sty_a", "backlog", 3, true); !b || !strings.Contains(msg, "3 commit") {
		t.Errorf("ungated-commits sub-message missing: b=%v msg=%q", b, msg)
	}
	if b, msg := stopGoalDecision("sty_a", "in_progress", 0, false); b || !strings.Contains(msg, "unreachable") {
		t.Errorf("ungoverned goal must NOT block and must report: b=%v msg=%q", b, msg)
	}
}

// TestRunHookStopCheck: the wrapper releases (no block) for a terminal story and
// for a non-terminal engagement in an UNGOVERNED repo (no workflow → unreachable
// → report, not loop), and is silent/non-blocking with no engagement.
func TestRunHookStopCheck(t *testing.T) {
	fresh := time.Now().UTC().Add(time.Hour)

	// Non-terminal engagement, ungoverned repo (no workflows) → report, no block.
	repo := writeRepo(t, true, "")
	seedEngagement(t, repo, "sess1", "sty_x", "in_progress", true, fresh)
	var out bytes.Buffer
	if block := runHookStopCheck(strings.NewReader(`{"session_id":"sess1","cwd":"`+repo+`"}`), &out); block {
		t.Errorf("ungoverned repo must not block (unreachable goal)")
	}
	if !strings.Contains(out.String(), "unreachable") {
		t.Errorf("expected an unreachable report, got %q", out.String())
	}

	// Terminal story → released, silent.
	repo2 := writeRepo(t, true, "")
	seedEngagement(t, repo2, "sess2", "sty_y", "done", true, fresh)
	var out2 bytes.Buffer
	if block := runHookStopCheck(strings.NewReader(`{"session_id":"sess2","cwd":"`+repo2+`"}`), &out2); block {
		t.Errorf("terminal story must not block")
	}

	// No engagement → released, silent.
	repo3 := writeRepo(t, true, "")
	var out3 bytes.Buffer
	if block := runHookStopCheck(strings.NewReader(`{"session_id":"sessZ","cwd":"`+repo3+`"}`), &out3); block {
		t.Errorf("no engagement must not block")
	}
}
