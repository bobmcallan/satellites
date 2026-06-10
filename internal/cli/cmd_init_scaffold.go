// Workflow scaffold for `satellites init` (epic:enforcement-surface, order:2,
// sty_a9621dc6). A fresh repo with no `## Workflow` and no reviewer-gate skills
// leaves the START door enforcing nothing — "engage a story" degrades to
// engaging a throwaway record that can never advance (Leak C). On init, when the
// repo has no kind:workflow source yet, write a minimal trunk workflow + its two
// gate skills into .satellites/skills/ so a story can actually be driven
// backlog → in_progress → done through real gates.
//
// This is a SCAFFOLD, not a runtime default (no-default-workflow): the files are
// operator-owned .satellites/ SOURCE the operator edits and uploads via the
// normal review-gated path. The binary still resolves no workflow itself — the
// story's embedded `## Workflow` remains the only authority a gate reads.
//
// Idempotent + non-clobbering: when any .satellites/skills/*.md already declares
// kind:workflow, nothing is written; an existing scaffold file is never
// overwritten.

package cli

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satellites/internal/frontmatter"
)

// scaffoldFS embeds the trunk workflow + gate skill templates. They are written
// verbatim into a fresh repo's .satellites/skills/ — bytes the operator owns and
// edits, never materialised into .claude/skills/ directly.
//
//go:embed scaffold/*.md
var scaffoldFS embed.FS

const scaffoldDir = "scaffold"

// scaffoldWorkflowIfAbsent writes the embedded trunk workflow + gate templates
// into <satDir>/skills/ when the repo has no kind:workflow source. It reports
// each file (added / already present) and a next-step pointer. A repo that
// already carries a workflow (e.g. an established project) is left untouched.
func scaffoldWorkflowIfAbsent(out io.Writer, satDir string) error {
	skillsDir := filepath.Join(satDir, "skills")
	has, err := hasWorkflowSource(skillsDir)
	if err != nil {
		return err
	}
	if has {
		fmt.Fprintln(out, initLine(false, ".satellites/skills/ (workflow already present — scaffold skipped)"))
		return nil
	}
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return fmt.Errorf("init: create %s: %w", skillsDir, err)
	}
	entries, err := fs.ReadDir(scaffoldFS, scaffoldDir)
	if err != nil {
		return fmt.Errorf("init: read embedded scaffold: %w", err)
	}
	wrote := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		dest := filepath.Join(skillsDir, e.Name())
		switch _, statErr := os.Stat(dest); {
		case statErr == nil:
			// Never clobber a file the operator may have edited.
			fmt.Fprintln(out, initLine(false, ".satellites/skills/"+e.Name()))
			continue
		case !os.IsNotExist(statErr):
			return fmt.Errorf("init: stat %s: %w", dest, statErr)
		}
		body, rerr := scaffoldFS.ReadFile(scaffoldDir + "/" + e.Name())
		if rerr != nil {
			return fmt.Errorf("init: read embedded %s: %w", e.Name(), rerr)
		}
		if werr := os.WriteFile(dest, body, 0o644); werr != nil {
			return fmt.Errorf("init: write %s: %w", dest, werr)
		}
		fmt.Fprintln(out, initLine(true, ".satellites/skills/"+e.Name()))
		wrote = true
	}
	if wrote {
		fmt.Fprintln(out, "  → next: review the scaffolded trunk workflow + gates, then `satellites skill upload` to push them (review-gated). Edit them to fit your process.")
	}
	return nil
}

// hasWorkflowSource reports whether any .satellites/skills/<name>.md declares
// kind:workflow in its frontmatter — the signal that the repo already defines a
// process. A missing skills dir means no workflow. A malformed file is ignored
// (it is not a usable workflow source).
func hasWorkflowSource(skillsDir string) (bool, error) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("init: read %s: %w", skillsDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(skillsDir, e.Name()))
		if rerr != nil {
			return false, fmt.Errorf("init: read %s: %w", e.Name(), rerr)
		}
		fm, _, perr := frontmatter.Parse(raw)
		if perr != nil {
			continue
		}
		if strings.TrimSpace(fm.Kind) == "workflow" {
			return true, nil
		}
	}
	return false, nil
}
