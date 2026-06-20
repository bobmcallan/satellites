// `satellites workflow list <story-id|category:X>` — the ranked governing-
// workflow PALETTE for a category (sty_ed12634d). The engine no longer DECIDES
// the one workflow; the agent SELECTS from this ranked list. It reuses the
// resolver's source set (config/workflows embed + .satellites/workflows +
// materialised kind:workflow skills) and the simple frontmatter ranking, then
// renders the candidates — the top row is the default the engine would pick.
// Read-only; it offers, it does not decide.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/spf13/cobra"
)

// workflowListRow is one candidate in a category's ranked palette.
type workflowListRow struct {
	Name        string   `json:"name"`
	AppliesTo   []string `json:"applies_to"`
	Source      string   `json:"source"` // local | skill | embed
	Description string   `json:"description"`
	Specific    bool     `json:"specific"` // true = specific applies_to match; false = wildcard ["*"]
	Default     bool     `json:"default"`  // the top-ranked row the engine would pick
}

func newWorkflowListCmd(configArg, userArg *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list <story-id|category:X>",
		Short: "List the workflows matching a category, ranked — the palette the agent selects from",
		Long: `list returns the workflows that govern a category — the ranked PALETTE an
agent selects from — drawn from the full source set: .satellites/workflows
(local), materialised kind:workflow skills, and the config/workflows embed
(system defaults).

The target is a story id (its category is resolved) or a literal category:X.

Ranking (the simple frontmatter rule): a SPECIFIC applies_to match ranks above a
wildcard ["*"]; within a tier, a local .satellites/workflows workflow ranks
before a materialised skill, before the embed. The top row (marked default) is
the one the engine would pick for that category. --json emits the structured list.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runWorkflowList(ctx, cmd.OutOrStdout(), *configArg, *userArg, strings.TrimSpace(args[0]), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the ranked palette as a JSON array")
	return cmd
}

// resolveListCategory resolves the list target to a category: a literal
// `category:X`, or a `sty_` id whose category is fetched from the server.
func resolveListCategory(ctx context.Context, configPath, userArg, target string) (string, error) {
	if rest, ok := strings.CutPrefix(target, "category:"); ok {
		cat := strings.TrimSpace(rest)
		if cat == "" {
			return "", fmt.Errorf("workflow list: empty category in %q", target)
		}
		return cat, nil
	}
	if strings.HasPrefix(target, "sty_") {
		story, _, err := reviewGetStory(ctx, reviewOpts{StoryID: target, ConfigPath: configPath, UserArg: userArg})
		if err != nil {
			return "", fmt.Errorf("workflow list: resolve story %s: %w", target, err)
		}
		cat := strings.TrimSpace(story.Category)
		if cat == "" {
			return "", fmt.Errorf("workflow list: story %s has no category to match", target)
		}
		return cat, nil
	}
	return "", fmt.Errorf("workflow list: %q is neither a sty_ id nor a category:X target", target)
}

// paletteFor builds the ranked palette for a category over the full source set,
// in resolver precedence order (local → skill → embed). Pure given its source
// collections, so the gathering (disk/embed I/O) stays in runWorkflowList and
// the ranking is fixture-testable.
func paletteFor(category string, local, skills, embed []matSkill) []workflowListRow {
	var rows []workflowListRow
	add := func(set []matSkill, source string) {
		for _, s := range set {
			if s.kind != "workflow" {
				continue
			}
			wf, err := workflow.Parse([]byte(s.raw))
			if err != nil || wf == nil {
				continue
			}
			specific, wildcard := false, false
			for _, at := range wf.AppliesTo {
				at = strings.TrimSpace(at)
				switch {
				case at == "*":
					wildcard = true
				case at != "" && strings.EqualFold(at, category):
					specific = true
				}
			}
			if !specific && !wildcard {
				continue
			}
			rows = append(rows, workflowListRow{
				Name:        s.name,
				AppliesTo:   wf.AppliesTo,
				Source:      source,
				Description: strings.TrimSpace(s.description),
				Specific:    specific,
			})
		}
	}
	add(local, "local")
	add(skills, "skill")
	add(embed, "embed")
	return rankPalette(rows)
}

// rankPalette orders a category's candidates by the simple frontmatter rule —
// specific applies_to before wildcard; within a tier, local before skill before
// embed — preserving source order within a (tier, source) group, and marks the
// top row as the engine default. Pure.
func rankPalette(rows []workflowListRow) []workflowListRow {
	sourceRank := map[string]int{"local": 0, "skill": 1, "embed": 2}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Specific != rows[j].Specific {
			return rows[i].Specific // a specific match ranks above a wildcard
		}
		return sourceRank[rows[i].Source] < sourceRank[rows[j].Source]
	})
	if len(rows) > 0 {
		rows[0].Default = true
	}
	return rows
}

func runWorkflowList(ctx context.Context, out io.Writer, configPath, userArg, target string, asJSON bool) error {
	category, err := resolveListCategory(ctx, configPath, userArg, target)
	if err != nil {
		return err
	}
	var matWF []matSkill
	for _, s := range materialisedSkills() {
		if s.kind == "workflow" {
			matWF = append(matWF, s)
		}
	}
	rows := paletteFor(category, clientWorkflows(configPath), matWF, embeddedWorkflows())

	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"category": category, "workflows": rows})
	}

	if len(rows) == 0 {
		fmt.Fprintf(out, "no workflow's applies_to covers category %q — author one under .satellites/workflows, or rely on the embedded baseline wildcard.\n", category)
		return nil
	}
	fmt.Fprintf(out, "workflows governing category %q (ranked; top is the default):\n\n", category)
	for _, r := range rows {
		marker := "  "
		if r.Default {
			marker = "→ "
		}
		match := "wildcard"
		if r.Specific {
			match = "specific"
		}
		fmt.Fprintf(out, "%s%-32s  applies_to=%-18s  %-6s  %-8s  %s\n",
			marker, r.Name, "["+strings.Join(r.AppliesTo, ",")+"]", r.Source, match, r.Description)
	}
	fmt.Fprintf(out, "\nselect one by name (record it as the story's workflow), then `satellites workflow show <name>`.\n")
	return nil
}
