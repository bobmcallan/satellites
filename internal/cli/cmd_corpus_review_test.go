package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelectReviewTargets pins the name-XOR-all contract (AC2): exactly one of
// <name> or --all is required, --all returns the whole set, and a name selects
// the single matching target or errors when none carries it.
func TestSelectReviewTargets(t *testing.T) {
	targets := []documentTarget{
		{Name: "alpha", Path: ".satellites/skills/alpha.md"},
		{Name: "beta", Path: ".satellites/skills/beta.md"},
	}

	t.Run("both set is an error", func(t *testing.T) {
		if _, err := selectReviewTargets(targets, "alpha", true); err == nil {
			t.Fatal("want error when both <name> and --all are set")
		}
	})
	t.Run("neither set is an error", func(t *testing.T) {
		if _, err := selectReviewTargets(targets, "", false); err == nil {
			t.Fatal("want error when neither <name> nor --all is set")
		}
	})
	t.Run("all returns every target", func(t *testing.T) {
		got, err := selectReviewTargets(targets, "", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("want 2 targets, got %d", len(got))
		}
	})
	t.Run("name selects the single match", func(t *testing.T) {
		got, err := selectReviewTargets(targets, "beta", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Name != "beta" {
			t.Fatalf("want only beta, got %+v", got)
		}
	})
	t.Run("unknown name is an error", func(t *testing.T) {
		if _, err := selectReviewTargets(targets, "gamma", false); err == nil {
			t.Fatal("want error for a name with no source")
		}
	})
}

// TestFormatReviewSummary pins the summary shape (AC3/AC4): per-artifact rows
// (verdict, findings, the revised path in apply mode) and a totals line that
// only reports "revised" outside dryrun.
func TestFormatReviewSummary(t *testing.T) {
	results := []reviewResult{
		{Name: "good", Verdict: "SHIP", Findings: "clean"},
		{Name: "bad", Path: ".satellites/skills/bad.md", Verdict: "REVISE", Findings: "fix the description", Revised: true},
		{Name: "broke", Err: "review pass: claude not found"},
	}

	t.Run("apply mode names the revised file and counts it", func(t *testing.T) {
		out := formatReviewSummary("skills", false, results)
		for _, want := range []string{
			"reviewed 3 skills artifact(s)\n",
			"good: SHIP",
			"bad: REVISE → revised .satellites/skills/bad.md",
			"    fix the description",
			"broke: error — review pass: claude not found",
			"totals: 3 reviewed · 1 SHIP · 1 REVISE · 1 error(s) · 1 revised",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("summary missing %q\n---\n%s", want, out)
			}
		}
	})

	t.Run("dryrun is tagged and omits revised", func(t *testing.T) {
		out := formatReviewSummary("skills", true, results)
		if !strings.Contains(out, "[dry-run]") {
			t.Errorf("dryrun header not tagged:\n%s", out)
		}
		if strings.Contains(out, "revised") {
			t.Errorf("dryrun summary must not mention revised:\n%s", out)
		}
		// A REVISE row in dryrun reports the verdict only, no path arrow.
		if !strings.Contains(out, "bad: REVISE\n") {
			t.Errorf("dryrun REVISE row should carry no path:\n%s", out)
		}
	})
}

// TestEngineDryRunMakesNoWrites pins AC3/AC5: under --dryrun a REVISE verdict
// triggers NO revise pass and the source file is byte-unchanged.
func TestEngineDryRunMakesNoWrites(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "alpha.md")
	const original = "---\nname: alpha\n---\n# alpha\noriginal body\n"
	if err := os.WriteFile(src, []byte(original), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	revised := false
	eng := corpusReviewEngine{
		kind:   "skills",
		dryRun: true,
		reviewFn: func(_ context.Context, _ string) (string, string, error) {
			return "REVISE", "needs work", nil
		},
		reviseFn: func(_ context.Context, _, _ string) (string, error) {
			revised = true
			return "REWRITTEN", nil
		},
	}

	var out strings.Builder
	if err := eng.run(context.Background(), &out, []documentTarget{{Name: "alpha", Path: src}}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if revised {
		t.Fatal("revise pass ran under --dryrun")
	}
	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reread source: %v", err)
	}
	if string(got) != original {
		t.Fatalf("source changed under --dryrun:\n%s", got)
	}
}

// TestEngineApplyRewritesRevise pins AC4: outside dryrun a REVISE artifact is
// rewritten in place with the revise pass output and reported as revised.
func TestEngineApplyRewritesRevise(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "alpha.md")
	if err := os.WriteFile(src, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	eng := corpusReviewEngine{
		kind:   "skills",
		dryRun: false,
		reviewFn: func(_ context.Context, _ string) (string, string, error) {
			return "REVISE", "fix it", nil
		},
		reviseFn: func(_ context.Context, _, _ string) (string, error) {
			return "rewritten\n", nil
		},
	}

	var out strings.Builder
	if err := eng.run(context.Background(), &out, []documentTarget{{Name: "alpha", Path: src}}); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if string(got) != "rewritten\n" {
		t.Fatalf("source not rewritten, got %q", got)
	}
	if !strings.Contains(out.String(), "revised "+src) {
		t.Errorf("summary should name the revised file:\n%s", out.String())
	}
}

// TestEngineFailSoft pins AC5: a review-pass error is recorded and the sweep
// continues to the next artifact rather than aborting.
func TestEngineFailSoft(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("body\n"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	eng := corpusReviewEngine{
		kind:   "skills",
		dryRun: true,
		reviewFn: func(_ context.Context, raw string) (string, string, error) {
			return "SHIP", "ok", nil
		},
	}
	// First target errors; the second must still be reviewed.
	calls := 0
	eng.reviewFn = func(_ context.Context, _ string) (string, string, error) {
		calls++
		if calls == 1 {
			return "", "", context.DeadlineExceeded
		}
		return "SHIP", "ok", nil
	}

	var out strings.Builder
	if err := eng.run(context.Background(), &out, []documentTarget{{Name: "a", Path: a}, {Name: "b", Path: b}}); err != nil {
		t.Fatalf("run returned error (should be fail-soft): %v", err)
	}
	if calls != 2 {
		t.Fatalf("sweep aborted early — want 2 review calls, got %d", calls)
	}
	s := out.String()
	if !strings.Contains(s, "a: error —") {
		t.Errorf("first artifact error not reported:\n%s", s)
	}
	if !strings.Contains(s, "b: SHIP") {
		t.Errorf("second artifact not reviewed after first errored:\n%s", s)
	}
}

// TestParseReviewVerdict pins verdict extraction from the review skill's prose:
// the last line naming exactly one of SHIP/REVISE wins; a line naming both is
// ambiguous and skipped; no verdict yields "".
func TestParseReviewVerdict(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"ship verdict", "looks good\n\nSHIP", "SHIP"},
		{"revise verdict", "needs work\n\nREVISE", "REVISE"},
		{"ambiguous instruction line skipped", "verdict: SHIP / REVISE\nactual: REVISE", "REVISE"},
		{"no verdict", "just prose, no decision", ""},
		{"last verdict wins", "REVISE earlier note\n\nfinal: SHIP", "SHIP"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := parseReviewVerdict(c.in)
			if got != c.want {
				t.Fatalf("parseReviewVerdict(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestAuthoringSkillForKind pins the kind→authoring-skill mapping the revise
// pass dispatches.
func TestAuthoringSkillForKind(t *testing.T) {
	if got := authoringSkillForKind("skills"); got != "satellites-skill-authoring" {
		t.Errorf("skills → %q", got)
	}
	if got := authoringSkillForKind("principles"); got != "satellites-principle-authoring" {
		t.Errorf("principles → %q", got)
	}
}
