package cli

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
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
	var out bytes.Buffer
	if err := runWorkInit(&out, repo, workDir, "sty_xyz", "in_progress", "2026-06-05T00:00:00Z"); err != nil {
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
	if err := runWorkInit(io.Discard, repo, workDir, "   ", "", "t"); err == nil {
		t.Errorf("empty story id should error")
	}
}
