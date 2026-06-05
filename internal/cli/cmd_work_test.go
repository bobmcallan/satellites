package cli

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunWorkInit_EngagesAndFlipsGate is the end-to-end AC: before init the
// START door denies; `work init` writes the engagement and the SAME gate then
// allows (writer and reader agree on the work dir).
func TestRunWorkInit_EngagesAndFlipsGate(t *testing.T) {
	repo := writeRepo(t, true, "") // configured, no engagement yet

	// Precondition: the door denies before engagement.
	if allow, _ := gateOutcome(repo); allow {
		t.Fatalf("precondition failed: gate should deny before `work init`")
	}

	workDir := filepath.Join(repo, ".satellites", "work")
	stateDB := filepath.Join(workDir, "state.db")
	now := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	if err := runWorkInit(&out, repo, workDir, stateDB, "sty_xyz", "in_progress", "sess1", now); err != nil {
		t.Fatalf("runWorkInit: %v", err)
	}

	// The engagement was written and names the story.
	eng, ok := readEngagement(workDir)
	if !ok || eng.StoryID != "sty_xyz" {
		t.Fatalf("engagement not written: %+v ok=%v", eng, ok)
	}
	if eng.Status != "in_progress" || eng.UpdatedAt == "" {
		t.Errorf("engagement missing status/updated_at: %+v", eng)
	}

	// AC3: the gate now allows — writer flipped reader.
	if allow, _ := gateOutcome(repo); !allow {
		t.Errorf("after `work init` the gate should allow, it still denies")
	}
	if !strings.Contains(out.String(), "sty_xyz") {
		t.Errorf("output should name the engaged story, got %q", out.String())
	}
}

// TestRunWorkInit_RequiresStory: an empty story id is an error (no engagement
// written).
func TestRunWorkInit_RequiresStory(t *testing.T) {
	repo := t.TempDir()
	workDir := filepath.Join(repo, ".satellites", "work")
	stateDB := filepath.Join(workDir, "state.db")
	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "   ", "", "sess1", time.Now().UTC()); err == nil {
		t.Errorf("empty story id should error")
	}
}

// TestRunWorkInit_SingleOpenPerSession: a session may hold one open story; a
// second different story under a fresh lease is refused; re-engaging the same
// story is allowed (refreshes the lease); `work close` frees the session (AC2/AC3).
func TestRunWorkInit_SingleOpenPerSession(t *testing.T) {
	repo := writeRepo(t, true, "")
	workDir := filepath.Join(repo, ".satellites", "work")
	stateDB := filepath.Join(workDir, "state.db")
	now := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)

	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "sty_aaa", "in_progress", "sess1", now); err != nil {
		t.Fatalf("first engage: %v", err)
	}
	// Same story again — allowed (lease refresh).
	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "sty_aaa", "in_progress", "sess1", now.Add(time.Minute)); err != nil {
		t.Fatalf("re-engage same story should be allowed: %v", err)
	}
	// Different story, same session, fresh lease — refused.
	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "sty_bbb", "in_progress", "sess1", now.Add(2*time.Minute)); err == nil {
		t.Fatalf("second different open story should be refused")
	}
	// A different SESSION is independent — allowed.
	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "sty_bbb", "in_progress", "sess2", now); err != nil {
		t.Fatalf("different session should be allowed: %v", err)
	}
	// Close sty_aaa for sess1, then sty_bbb is allowed there.
	if err := runWorkClose(io.Discard, repo, workDir, stateDB, "sty_aaa", "sess1", now.Add(3*time.Minute)); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "sty_bbb", "in_progress", "sess1", now.Add(4*time.Minute)); err != nil {
		t.Fatalf("after close, engaging a new story should be allowed: %v", err)
	}
}

// TestRunWorkClose_ClearsLegacyFile: close removes engagement.json when it names
// the closed story, so the legacy door no longer vouches (AC2).
func TestRunWorkClose_ClearsLegacyFile(t *testing.T) {
	repo := writeRepo(t, true, "")
	workDir := filepath.Join(repo, ".satellites", "work")
	stateDB := filepath.Join(workDir, "state.db")
	now := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)

	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "sty_ccc", "in_progress", "sess1", now); err != nil {
		t.Fatalf("engage: %v", err)
	}
	if _, ok := readEngagement(workDir); !ok {
		t.Fatalf("engagement.json should exist after init")
	}
	if err := runWorkClose(io.Discard, repo, workDir, stateDB, "sty_ccc", "sess1", now.Add(time.Minute)); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, ok := readEngagement(workDir); ok {
		t.Errorf("engagement.json should be cleared after close")
	}
}
