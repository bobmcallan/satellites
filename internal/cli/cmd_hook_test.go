package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRepo creates a satellites repo skeleton (with the toml) under a temp
// dir, optionally with an engagement, and returns the repo root.
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

// TestGateOutcome covers the three branches of the START door (story AC 2/3/4).
func TestGateOutcome(t *testing.T) {
	// Not configured (no toml) → deny, fail closed.
	bare := t.TempDir()
	if allow, reason := gateOutcome(bare); allow || !strings.Contains(reason, "satellites init") {
		t.Errorf("unconfigured repo: allow=%v reason=%q, want deny mentioning `satellites init`", allow, reason)
	}

	// Configured, no engagement → deny (the door), reason names work init.
	noEng := writeRepo(t, true, "")
	if allow, reason := gateOutcome(noEng); allow || !strings.Contains(reason, "work init") {
		t.Errorf("no engagement: allow=%v reason=%q, want deny mentioning `work init`", allow, reason)
	}

	// Configured, active engagement → allow.
	eng := writeRepo(t, true, "sty_abc123")
	if allow, _ := gateOutcome(eng); !allow {
		t.Errorf("active engagement: allow=false, want allow")
	}

	// Configured from a nested cwd → still resolves the engagement (walk-up).
	nested := filepath.Join(eng, "internal", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if allow, _ := gateOutcome(nested); !allow {
		t.Errorf("nested cwd under engaged repo: allow=false, want allow")
	}
}

// TestReadEngagement_EmptyStoryIsNotActive: a record with no story_id is not an
// active engagement (it must name a story).
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

// TestRunHookGate_DenyEmitsValidDecision: with no engagement, the gate writes a
// well-formed Claude Code PreToolUse deny decision.
func TestRunHookGate_DenyEmitsValidDecision(t *testing.T) {
	repo := writeRepo(t, true, "")
	in := bytes.NewBufferString(`{"tool_name":"Edit","cwd":"` + repo + `"}`)
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

// TestRunHookGate_AllowEmitsNothing: an engaged repo produces no output, so the
// tool proceeds through normal permissioning (the door blocks, never approves).
func TestRunHookGate_AllowEmitsNothing(t *testing.T) {
	repo := writeRepo(t, true, "sty_live")
	in := bytes.NewBufferString(`{"tool_name":"Write","cwd":"` + repo + `"}`)
	var out bytes.Buffer
	if err := runHookGate(in, &out); err != nil {
		t.Fatalf("runHookGate: %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("engaged repo should emit nothing (allow), got %q", out.String())
	}
}
