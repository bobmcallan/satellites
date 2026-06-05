package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/workstate"
)

// writeRepo creates a satellites repo skeleton (with the toml) under a temp dir,
// optionally with a legacy engagement.json, and returns the repo root. The door
// no longer reads engagement.json — store engagements are seeded with
// seedEngagement — but the file is kept for the work-command tests.
func writeRepo(t *testing.T, withToml bool, engagementStory string) string {
	t.Helper()
	root := t.TempDir()
	if withToml {
		sat := filepath.Join(root, ".satellites")
		if err := os.MkdirAll(sat, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sat, "satellites.toml"), []byte("server_url=\"x\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if engagementStory != "" {
		work := filepath.Join(root, ".satellites", "work")
		if err := os.MkdirAll(work, 0o755); err != nil {
			t.Fatal(err)
		}
		body := []byte(`{"story_id":"` + engagementStory + `","status":"in_progress"}`)
		if err := os.WriteFile(filepath.Join(work, "engagement.json"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// seedEngagement writes a store engagement at the repo's resolved state.db — the
// authority the door reads.
func seedEngagement(t *testing.T, repo, session, story, phase string, editable bool, leaseUntil time.Time) {
	t.Helper()
	s, err := workstate.Open(stateDBForRoot(repo))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Append(workstate.Event{
		Session: session, Story: story, Phase: phase, Kind: "engage",
		LeaseUntil: leaseUntil, Editable: editable, TS: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

// TestGateOutcome covers the store-based door (sty_2b6cd041 AC1–AC5): allow only
// under a lease-fresh, editable engagement for the editing session.
func TestGateOutcome(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(time.Hour)

	// Unconfigured → deny, fail closed.
	if allow, reason := gateOutcome(t.TempDir(), "s", now); allow || !strings.Contains(reason, "satellites init") {
		t.Errorf("unconfigured: allow=%v reason=%q", allow, reason)
	}

	// Configured, no engagement → deny (divert to work init).
	noEng := writeRepo(t, true, "")
	if allow, reason := gateOutcome(noEng, "sess1", now); allow || !strings.Contains(reason, "work init") {
		t.Errorf("no engagement: allow=%v reason=%q", allow, reason)
	}

	// Lease-fresh + editable → allow; a different session is not.
	ok := writeRepo(t, true, "")
	seedEngagement(t, ok, "sess1", "sty_a", "in_progress", true, fresh)
	if allow, _ := gateOutcome(ok, "sess1", now); !allow {
		t.Errorf("lease-fresh editable: want allow")
	}
	if allow, _ := gateOutcome(ok, "other", now); allow {
		t.Errorf("a different session must not ride sess1's engagement")
	}

	// Expired lease → deny (the VIRE bug).
	exp := writeRepo(t, true, "")
	seedEngagement(t, exp, "sess1", "sty_a", "in_progress", true, now.Add(-time.Hour))
	if allow, reason := gateOutcome(exp, "sess1", now); allow || !strings.Contains(reason, "lease") {
		t.Errorf("expired lease: allow=%v reason=%q, want deny mentioning lease", allow, reason)
	}

	// Non-editable phase → deny.
	ned := writeRepo(t, true, "")
	seedEngagement(t, ned, "sess1", "sty_a", "backlog", false, fresh)
	if allow, reason := gateOutcome(ned, "sess1", now); allow || !strings.Contains(reason, "editable") {
		t.Errorf("non-editable phase: allow=%v reason=%q, want deny mentioning editable", allow, reason)
	}

	// Candidate (access-only) row → deny.
	cand := writeRepo(t, true, "")
	seedEngagement(t, cand, "sess1", "sty_a", phaseCandidate, false, fresh)
	if allow, _ := gateOutcome(cand, "sess1", now); allow {
		t.Errorf("candidate row must not authorise edits")
	}

	// Nested cwd resolves the repo via walk-up.
	nested := filepath.Join(ok, "internal", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if allow, _ := gateOutcome(nested, "sess1", now); !allow {
		t.Errorf("nested cwd under engaged repo: want allow")
	}
}

// TestReadEngagement_EmptyStoryIsNotActive: the legacy engagement.json reader
// (still used by the work commands) rejects a record with no story_id.
func TestReadEngagement_EmptyStoryIsNotActive(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, ".satellites", "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "engagement.json"), []byte(`{"story_id":"","status":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readEngagement(work); ok {
		t.Errorf("engagement with empty story_id must not be active")
	}
}

// TestRunHookGate_DenyEmitsValidDecision: no engagement ⇒ a well-formed deny.
func TestRunHookGate_DenyEmitsValidDecision(t *testing.T) {
	repo := writeRepo(t, true, "")
	in := bytes.NewBufferString(`{"tool_name":"Edit","cwd":"` + repo + `","session_id":"sess1"}`)
	var out bytes.Buffer
	if err := runHookGate(in, &out); err != nil {
		t.Fatalf("runHookGate: %v", err)
	}
	var dec gateDecisionJSON
	if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if dec.HookSpecificOutput.HookEventName != "PreToolUse" || dec.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("decision = %+v, want PreToolUse/deny", dec.HookSpecificOutput)
	}
	if !strings.Contains(dec.HookSpecificOutput.PermissionDecisionReason, "work init") {
		t.Errorf("reason %q should divert to `work init`", dec.HookSpecificOutput.PermissionDecisionReason)
	}
}

// TestRunHookGate_AllowEmitsNothing: a lease-fresh editable engagement for the
// payload's session ⇒ no output (allow).
func TestRunHookGate_AllowEmitsNothing(t *testing.T) {
	repo := writeRepo(t, true, "")
	seedEngagement(t, repo, "sess1", "sty_live", "in_progress", true, time.Now().UTC().Add(time.Hour))
	in := bytes.NewBufferString(`{"tool_name":"Write","cwd":"` + repo + `","session_id":"sess1"}`)
	var out bytes.Buffer
	if err := runHookGate(in, &out); err != nil {
		t.Fatalf("runHookGate: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("engaged repo should emit nothing (allow), got %q", out.String())
	}
}
