// `satellites workflow show <name|story-id> [--dot]` — render a workflow
// definition (epic:graduated-workflow dot-export-patterns-doc). The yaml IS
// the execution format; DOT is an EXPORT for visualisation only — pipe it to
// `dot -Tsvg`. Resolves either a materialised kind:workflow skill by name or
// a story's embedded `## Workflow` by id. Read-only.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/spf13/cobra"
)

func newWorkflowShowCmd(configArg, userArg *string) *cobra.Command {
	var asDOT bool
	cmd := &cobra.Command{
		Use:   "show <name|story-id>",
		Short: "Render a workflow (skill by name, or a story's embedded ## Workflow by sty_ id) — --dot emits Graphviz",
		Long: `show renders a workflow definition: a materialised kind:workflow skill by
name, or a story's embedded ` + "`## Workflow`" + ` by its sty_ id.

Default output is a readable text table (states with actors/commands;
transitions with gates, on-edges, bounds, exhaustion targets). --dot emits
Graphviz DOT — actors shape the nodes, pass edges are solid, fail edges dashed
with their ×N bound, exhaustion edges dotted, checkpoint gates render as side
nodes. DOT is an export for visualisation, never the execution format:

  satellites workflow show satellites-workflow --dot | dot -Tsvg > workflow.svg`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runWorkflowShow(ctx, cmd.OutOrStdout(), *configArg, *userArg, strings.TrimSpace(args[0]), asDOT)
		},
	}
	cmd.Flags().BoolVar(&asDOT, "dot", false, "Emit Graphviz DOT instead of the text table (pipe to `dot -Tsvg`)")
	return cmd
}

func runWorkflowShow(ctx context.Context, out io.Writer, configPath, userArg, target string, asDOT bool) error {
	wf, gates, err := resolveShowTarget(ctx, configPath, userArg, target)
	if err != nil {
		return err
	}
	if asDOT {
		fmt.Fprint(out, workflowDOT(wf, gates))
		return nil
	}
	printWorkflowTable(out, wf, gates)
	return nil
}

// resolveShowTarget resolves the show argument: a sty_ id loads the story's
// embedded workflow; anything else is a materialised skill name (the same
// corpus `workflow check` validates). Returns the parsed workflow plus the
// checkpoint gates the definition names (skills only; stories inherit the
// gates from their governing definition's body, which the embedded block does
// not carry).
func resolveShowTarget(ctx context.Context, configPath, userArg, target string) (*workflow.Workflow, []string, error) {
	if strings.HasPrefix(target, "sty_") {
		req, err := json.Marshal(verb.DocumentGetRequest{ID: target})
		if err != nil {
			return nil, nil, err
		}
		raw, err := dispatchVerb(ctx, "document_get", req, configPath, userArg)
		if err != nil {
			return nil, nil, fmt.Errorf("workflow show: resolve story %s: %w", target, err)
		}
		var resp verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, nil, err
		}
		body := resp.RawBody
		if body == "" && len(resp.Versions) > 0 {
			body = resp.Versions[0].Body
		}
		wf, perr := workflow.ParseBody([]byte(body))
		if perr != nil {
			return nil, nil, fmt.Errorf("workflow show: story %s embeds no parseable ## Workflow: %w", target, perr)
		}
		if wf.Name == "" {
			wf.Name = target
		}
		return wf, nil, nil
	}
	// Client-dir workflows first (they win an applies_to tie at resolution, so
	// `show` should render the same definition the dispatcher would enact), then
	// the materialised kind:workflow skills.
	for _, s := range append(clientWorkflows(configPath), materialisedSkills()...) {
		if s.name != target {
			continue
		}
		wf, perr := workflow.Parse([]byte(s.raw))
		if perr != nil {
			return nil, nil, fmt.Errorf("workflow show: %q carries no parseable workflow: %w", target, perr)
		}
		return wf, checkpointGates(s.body), nil
	}
	return nil, nil, fmt.Errorf("workflow show: %q is neither a sty_ id nor a workflow under .satellites/workflows or .claude/skills", target)
}

func printWorkflowTable(out io.Writer, wf *workflow.Workflow, gates []string) {
	fmt.Fprintf(out, "workflow: %s\n\nstates:\n", wf.Name)
	for _, s := range wf.States {
		line := "  " + s.Name
		if s.Actor != "" {
			line += "  (actor: " + s.Actor
			if s.Command != "" {
				line += ", command: " + s.Command
			}
			line += ")"
		}
		fmt.Fprintln(out, line)
	}
	fmt.Fprintln(out, "\ntransitions:")
	for _, t := range wf.Transitions {
		line := fmt.Sprintf("  %s → %s", t.From, t.To)
		var attrs []string
		if t.On != "" {
			attrs = append(attrs, "on: "+t.On)
		}
		if t.WorkSkill != "" {
			attrs = append(attrs, "work: "+t.WorkSkill)
		}
		if t.ReviewerSkill != "" {
			attrs = append(attrs, "gate: "+t.ReviewerSkill)
		}
		if t.Trigger != "" {
			attrs = append(attrs, "trigger: "+t.Trigger)
		}
		if t.MaxIterations > 0 {
			attrs = append(attrs, fmt.Sprintf("max_iterations: %d", t.MaxIterations))
		}
		if t.OnExhausted != "" {
			attrs = append(attrs, "on_exhausted: "+t.OnExhausted)
		}
		if len(attrs) > 0 {
			line += "  [" + strings.Join(attrs, ", ") + "]"
		}
		fmt.Fprintln(out, line)
	}
	if len(gates) > 0 {
		fmt.Fprintf(out, "\ncheckpoint gates: %s\n", strings.Join(gates, ", "))
	}
}

// actorShapes maps the reserved actor vocabulary onto node shapes/colours.
// Unknown actors fall back to a neutral shape with the actor named in the
// label — the vocabulary stays open.
var actorShapes = map[string][2]string{
	"executor":   {"box", "#dbeafe"},
	"reviewer":   {"diamond", "#fef3c7"},
	"satellites": {"component", "#dcfce7"},
	"operator":   {"house", "#fee2e2"},
}

// dotQuote renders a DOT double-quoted string: backslashes and quotes are
// escaped, newlines become DOT line breaks (\n) — %q would double-escape
// them into literal backslash-n.
func dotQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return `"` + s + `"`
}

// workflowDOT renders the workflow as Graphviz DOT — pure, fixture-testable.
func workflowDOT(wf *workflow.Workflow, checkpointGates []string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("digraph %q {\n", wf.Name))
	b.WriteString("  rankdir=LR;\n  node [fontname=\"Helvetica\"];\n")
	for _, s := range wf.States {
		shape, fill := "ellipse", "#f3f4f6"
		if sc, ok := actorShapes[s.Actor]; ok {
			shape, fill = sc[0], sc[1]
		}
		label := s.Name
		if s.Actor != "" {
			label += "\n(" + s.Actor + ")"
		}
		b.WriteString(fmt.Sprintf("  %q [label=%s, shape=%s, style=filled, fillcolor=%q];\n", s.Name, dotQuote(label), shape, fill))
	}
	for _, t := range wf.Transitions {
		var attrs []string
		label := t.ReviewerSkill
		switch t.On {
		case "pass":
			if label == "" {
				label = "pass"
			} else {
				label += " (pass)"
			}
		case "fail":
			attrs = append(attrs, "style=dashed")
			label = strings.TrimSpace(label + " fail")
			if t.MaxIterations > 0 {
				label += fmt.Sprintf(" ×%d", t.MaxIterations)
			}
		}
		if t.Trigger != "" {
			label = strings.TrimSpace(label + " ⚑" + t.Trigger)
		}
		if label != "" {
			attrs = append(attrs, fmt.Sprintf("label=%q", label))
		}
		b.WriteString(fmt.Sprintf("  %q -> %q", t.From, t.To))
		if len(attrs) > 0 {
			b.WriteString(" [" + strings.Join(attrs, ", ") + "]")
		}
		b.WriteString(";\n")
		if t.OnExhausted != "" {
			b.WriteString(fmt.Sprintf("  %q -> %q [style=dotted, color=\"#b91c1c\", label=\"exhausted\"];\n", t.From, t.OnExhausted))
		}
	}
	if len(checkpointGates) > 0 {
		gates := append([]string(nil), checkpointGates...)
		sort.Strings(gates)
		entry := wf.InitialState()
		for _, g := range gates {
			node := "gate:" + g
			b.WriteString(fmt.Sprintf("  %q [label=%q, shape=note, style=filled, fillcolor=\"#f5f3ff\"];\n", node, g))
			if entry != "" {
				b.WriteString(fmt.Sprintf("  %q -> %q [style=dotted, arrowhead=none, label=\"checkpoint\"];\n", node, entry))
			}
		}
	}
	b.WriteString("}\n")
	return b.String()
}
