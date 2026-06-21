package cli

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/workflow"
)

// v2 sketch from the epic anchor + a checkpoint gate — the DOT fixture.
func dotFixture(t *testing.T) *workflow.Workflow {
	t.Helper()
	wf, err := workflow.ParseBody([]byte("# t\n\n```yaml\n" +
		"states:\n" +
		"  - {name: in_progress,     actor: executor}\n" +
		"  - {name: techdebt-review, actor: satellites, command: \"true\"}\n" +
		"  - {name: done-review,     actor: reviewer}\n" +
		"  - {name: blocked,         actor: operator}\n" +
		"  - done\n" +
		"transitions:\n" +
		"  - {from: in_progress,     to: techdebt-review, trigger: checkpoint}\n" +
		"  - {from: techdebt-review, on: pass, to: done-review}\n" +
		"  - {from: techdebt-review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked}\n" +
		"  - {from: done-review,     on: pass, to: done}\n" +
		"  - {from: done-review,     on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked}\n" +
		"```\n"))
	if err != nil {
		t.Fatalf("fixture parse: %v", err)
	}
	wf.Name = "v2-sketch"
	return wf
}

// TestWorkflowDOT pins the export's structure (AC1): actors shape nodes,
// fail edges dash with their ×N bound, exhaustion edges dot into the
// escalation state, checkpoint gates render as side nodes — and when a `dot`
// binary exists, the output must parse through it.
func TestWorkflowDOT(t *testing.T) {
	out := workflowDOT(dotFixture(t), []string{"crater-scan"})
	for _, want := range []string{
		`digraph "v2-sketch" {`,
		`"in_progress" [label="in_progress\n(executor)", shape=box`,
		`"done-review" [label="done-review\n(reviewer)", shape=diamond`,
		`"techdebt-review" [label="techdebt-review\n(satellites)", shape=component`,
		`"blocked" [label="blocked\n(operator)", shape=house`,
		`style=dashed`, `fail ×3`,
		`"done-review" -> "blocked" [style=dotted`, `label="exhausted"`,
		`"gate:crater-scan"`, `shape=note`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("DOT missing %q\n%s", want, out)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "}") {
		t.Errorf("DOT not closed: %q", out[len(out)-20:])
	}
	// "Parses with dot" — pinned for real wherever graphviz is installed.
	dotBin, err := exec.LookPath("dot")
	if err != nil {
		t.Skip("graphviz dot not on PATH — structural assertions only")
	}
	cmd := exec.Command(dotBin, "-Tsvg", "-o", "/dev/null")
	cmd.Stdin = strings.NewReader(out)
	if msg, rerr := cmd.CombinedOutput(); rerr != nil {
		t.Fatalf("dot rejected the export: %v\n%s", rerr, msg)
	}
}

// TestPrintWorkflowTable pins the default render: states with actors and
// commands, transitions with their v2 attributes, checkpoint gates listed.
func TestPrintWorkflowTable(t *testing.T) {
	var b strings.Builder
	printWorkflowTable(&b, dotFixture(t), []string{"crater-scan"})
	out := b.String()
	for _, want := range []string{
		"workflow: v2-sketch",
		"techdebt-review  (actor: satellites, command: true)",
		"in_progress → techdebt-review  [trigger: checkpoint]",
		"techdebt-review → in_progress  [on: fail, max_iterations: 3, on_exhausted: blocked]",
		"checkpoint gates: crater-scan",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n%s", want, out)
		}
	}
}

// TestPrintWorkflowTable_WorkSkill pins epic:workflow-steps story 2 AC#3: a
// transition's work_skill (the step's "do" half) is surfaced in the render so
// the executor sees the skill to run, alongside the gate.
func TestPrintWorkflowTable_WorkSkill(t *testing.T) {
	wf, err := workflow.ParseBody([]byte("# t\n\n```yaml\n" +
		"states: [shipping, done]\n" +
		"transitions:\n" +
		"  - {from: shipping, to: done, work_skill: commit-push, reviewer_skill: \"satellites-commit-push-review\"}\n" +
		"```\n"))
	if err != nil {
		t.Fatalf("fixture parse: %v", err)
	}
	var b strings.Builder
	printWorkflowTable(&b, wf, nil)
	want := "shipping → done  [work: commit-push, gate: satellites-commit-push-review]"
	if !strings.Contains(b.String(), want) {
		t.Errorf("table missing %q\n%s", want, b.String())
	}
}
