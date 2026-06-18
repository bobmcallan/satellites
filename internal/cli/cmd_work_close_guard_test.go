package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/workstate"
)

// gitExec runs a git command in dir, failing the test on error.
func gitExec(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitRepoWithSatellites builds a temp git repo (one base commit) that is also a
// satellites repo, and returns its root.
func gitRepoWithSatellites(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitExec(t, repo, "init")
	gitExec(t, repo, "commit", "--allow-empty", "-m", "base")
	sat := filepath.Join(repo, ".satellites")
	if err := os.MkdirAll(sat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sat, "satellites.toml"), []byte("server_url=\"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// seedEngagementAt records an engage event with an explicit TS (so UpdatedAt is
// controllable for the commits-since check).
func seedEngagementAt(t *testing.T, repo, session, story, phase string, editable bool, ts, lease time.Time) {
	t.Helper()
	s, err := workstate.Open(stateDBForRoot(repo))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Append(workstate.Event{
		Session: session, Story: story, Phase: phase, Kind: "engage",
		LeaseUntil: lease, Editable: editable, TS: ts.UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

// TestWorkCloseGuard pins AC1: close refuses a non-terminal story with commits
// since engagement; --force overrides; a terminal story or no-commits closes.
func TestWorkCloseGuard(t *testing.T) {
	repo := gitRepoWithSatellites(t)
	// The guard derives terminal-ness from the governing workflow (no hardcoded
	// names), so the test repo must be governed — the baseline declares
	// in_progress non-terminal and done terminal.
	if _, err := ensureBaselineWorkflow(repo); err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(repo, ".satellites", "work")
	stateDB := cliconfig.Config{}.ResolveStateDB(repo)
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	lease := now.Add(time.Hour)

	t.Run("refuses non-terminal with commits", func(t *testing.T) {
		seedEngagementAt(t, repo, "sess1", "sty_x", "in_progress", true, past, lease)
		gitExec(t, repo, "commit", "--allow-empty", "-m", "work after engage")
		err := runWorkClose(io.Discard, repo, workDir, stateDB, "sty_x", "sess1", false, now)
		if err == nil {
			t.Fatal("expected refusal: non-terminal story with commits")
		}
	})

	t.Run("force overrides", func(t *testing.T) {
		if err := runWorkClose(io.Discard, repo, workDir, stateDB, "sty_x", "sess1", true, now); err != nil {
			t.Fatalf("--force should close: %v", err)
		}
	})

	t.Run("terminal story closes despite commits", func(t *testing.T) {
		seedEngagementAt(t, repo, "sess2", "sty_done", "done", false, past, lease)
		gitExec(t, repo, "commit", "--allow-empty", "-m", "more work")
		if err := runWorkClose(io.Discard, repo, workDir, stateDB, "sty_done", "sess2", false, now); err != nil {
			t.Fatalf("terminal story should close: %v", err)
		}
	})

	t.Run("no commits since engagement closes", func(t *testing.T) {
		// Engage with a FUTURE timestamp so no commit counts as "since".
		seedEngagementAt(t, repo, "sess3", "sty_clean", "in_progress", true, now.Add(time.Hour), now.Add(2*time.Hour))
		if err := runWorkClose(io.Discard, repo, workDir, stateDB, "sty_clean", "sess3", false, now); err != nil {
			t.Fatalf("no-commit story should close: %v", err)
		}
	})
}

// TestWorkCloseGuard_RenamedTerminal pins epic:minimal-spine order-7: terminal-ness
// is engine-derived, so a repo whose governing workflow RENAMES its terminal state
// (here `shipped`, not `done`/`cancelled`) is honoured — the hardcoded-name
// regression is foreclosed. A non-terminal renamed state (`wip`) still blocks.
func TestWorkCloseGuard_RenamedTerminal(t *testing.T) {
	repo := gitRepoWithSatellites(t)
	wfDir := filepath.Join(repo, ".satellites", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	renamed := "---\nname: renamed-wf\nkind: workflow\napplies_to: [\"*\"]\n---\n" +
		"```yaml\n" +
		"states:\n  - backlog\n  - {name: wip, actor: executor}\n  - shipped\n" +
		"transitions:\n  - {from: backlog, to: wip}\n  - {from: wip, to: shipped}\n" +
		"```\n"
	if err := os.WriteFile(filepath.Join(wfDir, "renamed.md"), []byte(renamed), 0o644); err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(repo, ".satellites", "work")
	stateDB := cliconfig.Config{}.ResolveStateDB(repo)
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	lease := now.Add(time.Hour)

	t.Run("renamed terminal closes despite commits", func(t *testing.T) {
		seedEngagementAt(t, repo, "sessS", "sty_ship", "shipped", false, past, lease)
		gitExec(t, repo, "commit", "--allow-empty", "-m", "after engage")
		if err := runWorkClose(io.Discard, repo, workDir, stateDB, "sty_ship", "sessS", false, now); err != nil {
			t.Fatalf("a renamed terminal state (shipped) must close: %v", err)
		}
	})

	t.Run("renamed non-terminal with commits is refused", func(t *testing.T) {
		seedEngagementAt(t, repo, "sessW", "sty_wip", "wip", true, past, lease)
		gitExec(t, repo, "commit", "--allow-empty", "-m", "after engage 2")
		if err := runWorkClose(io.Discard, repo, workDir, stateDB, "sty_wip", "sessW", false, now); err == nil {
			t.Fatal("a renamed non-terminal state (wip) with commits must be refused")
		}
	})
}

// TestStopCheckGoalKeeper: the Stop hook BLOCKS a non-terminal engagement in a
// governed repo (re-injecting the goal), stays silent + releases for a terminal
// one, and never errors (epic:satellites-backbone 1.2).
func TestStopCheckGoalKeeper(t *testing.T) {
	repo := gitRepoWithSatellites(t)
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	lease := now.Add(time.Hour)
	gitExec(t, repo, "commit", "--allow-empty", "-m", "work")

	t.Run("blocks non-terminal in a governed repo", func(t *testing.T) {
		if _, err := ensureBaselineWorkflow(repo); err != nil { // make the goal reachable
			t.Fatal(err)
		}
		seedEngagementAt(t, repo, "sessA", "sty_open", "in_progress", true, past, lease)
		in := bytes.NewBufferString(`{"session_id":"sessA","cwd":"` + repo + `"}`)
		var out bytes.Buffer
		if block := runHookStopCheck(in, &out); !block {
			t.Fatalf("a non-terminal engaged story in a governed repo must block the stop")
		}
		if !bytes.Contains(out.Bytes(), []byte("sty_open")) || !bytes.Contains(out.Bytes(), []byte("not done")) {
			t.Errorf("expected the re-injected goal naming the story, got %q", out.String())
		}
	})

	t.Run("silent for terminal story", func(t *testing.T) {
		seedEngagementAt(t, repo, "sessB", "sty_fin", "done", false, past, lease)
		in := bytes.NewBufferString(`{"session_id":"sessB","cwd":"` + repo + `"}`)
		var out bytes.Buffer
		if block := runHookStopCheck(in, &out); block {
			t.Fatalf("terminal story must not block")
		}
		if out.Len() != 0 {
			t.Errorf("terminal story should produce no output, got %q", out.String())
		}
	})

	t.Run("silent when unconfigured", func(t *testing.T) {
		in := bytes.NewBufferString(`{"session_id":"sessC","cwd":"` + t.TempDir() + `"}`)
		var out bytes.Buffer
		if block := runHookStopCheck(in, &out); block {
			t.Fatalf("unconfigured repo must not block")
		}
		if out.Len() != 0 {
			t.Errorf("unconfigured repo should be silent, got %q", out.String())
		}
	})
}
