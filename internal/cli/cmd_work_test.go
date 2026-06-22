package cli

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
)

// TestRunWorkInit_EngagesAndFlipsGate: before init the store-based door denies;
// `work init` records a lease-fresh editable engagement and the SAME gate then
// allows for that session.
func TestRunWorkInit_EngagesAndFlipsGate(t *testing.T) {
	repo := writeRepo(t, true, "") // configured, no engagement yet
	workDir := filepath.Join(repo, ".satellites", "work")
	stateDB := cliconfig.Config{}.ResolveStateDB(repo)
	now := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)

	// Precondition: the door denies before engagement.
	if allow, _ := gateOutcome(repo, "sess1", "", now); allow {
		t.Fatalf("precondition failed: gate should deny before `work init`")
	}

	var out bytes.Buffer
	if err := runWorkInit(&out, repo, workDir, stateDB, "sty_xyz", "in_progress", "sess1", true, false, now); err != nil {
		t.Fatalf("runWorkInit: %v", err)
	}

	// The legacy engagement.json was written and names the story.
	eng, ok := readEngagement(workDir)
	if !ok || eng.StoryID != "sty_xyz" {
		t.Fatalf("engagement not written: %+v ok=%v", eng, ok)
	}

	// The gate now allows for this session — writer flipped reader.
	if allow, _ := gateOutcome(repo, "sess1", "", now); !allow {
		t.Errorf("after `work init` the gate should allow, it still denies")
	}
	// A different session is still denied.
	if allow, _ := gateOutcome(repo, "other", "", now); allow {
		t.Errorf("a different session must not be allowed by sess1's engagement")
	}
	if !strings.Contains(out.String(), "sty_xyz") {
		t.Errorf("output should name the engaged story, got %q", out.String())
	}
}

// TestRunWorkInit_RequiresStory: an empty story id is an error.
func TestRunWorkInit_RequiresStory(t *testing.T) {
	repo := t.TempDir()
	workDir := filepath.Join(repo, ".satellites", "work")
	stateDB := cliconfig.Config{}.ResolveStateDB(repo)
	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "   ", "", "sess1", true, false, time.Now().UTC()); err == nil {
		t.Errorf("empty story id should error")
	}
}

// TestRunWorkInit_SingleOpenPerSession: a session may hold one open story; a
// second different story under a fresh lease is refused; re-engaging the same
// story is allowed; `work close` frees the session.
func TestRunWorkInit_SingleOpenPerSession(t *testing.T) {
	repo := writeRepo(t, true, "")
	workDir := filepath.Join(repo, ".satellites", "work")
	stateDB := cliconfig.Config{}.ResolveStateDB(repo)
	now := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)

	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "sty_aaa", "in_progress", "sess1", true, false, now); err != nil {
		t.Fatalf("first engage: %v", err)
	}
	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "sty_aaa", "in_progress", "sess1", true, false, now.Add(time.Minute)); err != nil {
		t.Fatalf("re-engage same story should be allowed: %v", err)
	}
	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "sty_bbb", "in_progress", "sess1", true, false, now.Add(2*time.Minute)); err == nil {
		t.Fatalf("second different open story should be refused")
	}
	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "sty_bbb", "in_progress", "sess2", true, false, now); err != nil {
		t.Fatalf("different session should be allowed: %v", err)
	}
	if err := runWorkClose(io.Discard, repo, workDir, stateDB, "sty_aaa", "sess1", false, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "sty_bbb", "in_progress", "sess1", true, false, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("after close, engaging a new story should be allowed: %v", err)
	}
}

// TestRunWorkClose_ClearsLegacyFile: close removes engagement.json when it names
// the closed story.
func TestRunWorkClose_ClearsLegacyFile(t *testing.T) {
	repo := writeRepo(t, true, "")
	workDir := filepath.Join(repo, ".satellites", "work")
	stateDB := cliconfig.Config{}.ResolveStateDB(repo)
	now := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)

	if err := runWorkInit(io.Discard, repo, workDir, stateDB, "sty_ccc", "in_progress", "sess1", true, false, now); err != nil {
		t.Fatalf("engage: %v", err)
	}
	if _, ok := readEngagement(workDir); !ok {
		t.Fatalf("engagement.json should exist after init")
	}
	if err := runWorkClose(io.Discard, repo, workDir, stateDB, "sty_ccc", "sess1", false, now.Add(time.Minute)); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, ok := readEngagement(workDir); ok {
		t.Errorf("engagement.json should be cleared after close")
	}
}

// TestRefuseNonExecutorActor pins the actor handoff stop (AC3,
// epic:graduated-workflow dispatcher story): a non-executor state refuses
// the executor action naming whose turn it is; empty/executor pass.
func TestRefuseNonExecutorActor(t *testing.T) {
	if err := refuseNonExecutorActor("work init", "sty_x", "in_progress", ""); err != nil {
		t.Fatalf("empty actor must pass: %v", err)
	}
	if err := refuseNonExecutorActor("work init", "sty_x", "in_progress", "executor"); err != nil {
		t.Fatalf("executor actor must pass: %v", err)
	}
	err := refuseNonExecutorActor("work init", "sty_x", "techdebt-review", "satellites")
	if err == nil {
		t.Fatal("satellites actor must refuse")
	}
	for _, want := range []string{"satellites", "techdebt-review", "stop"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal must name %q: %v", want, err)
		}
	}
	if err := refuseNonExecutorActor("work init", "sty_x", "blocked", "operator"); err == nil {
		t.Fatal("operator actor must refuse")
	}
}
