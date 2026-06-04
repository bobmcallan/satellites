// `satellites skill index` — the dynamic dispatch index (sty_815c09e7).
//
// The index is the projection of every type:skill row's frontmatter
// (name / kind / applies_to / when / description) across the applicable
// scopes, bodies EXCLUDED — context-safe. It is what the agent's global rule
// (reviewer-only-model) consumes to pick a skill by story type + status, and
// what the gate selects the workflow skill from: applies_to is the single
// source, replacing the project-config story_types mapping.
//
// Composes the existing document_list / document_get verbs (it reuses
// listSubstrateSkills) — no new MCP verb (no-new-mcp-verbs).

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/spf13/cobra"
)

// skillIndexEntry is one skill's dispatch projection — its frontmatter
// dispatch fields plus the materialised local name, never its body.
type skillIndexEntry struct {
	Name        string
	Kind        string
	AppliesTo   []string
	When        string
	Description string
	LocalName   string // satellites-<name>: the materialised .claude/skills dir
}

// buildSkillIndex pulls the type:skill rows for the scope and projects each
// body's frontmatter into a dispatch entry, dropping the body. Deterministic
// order for stable output + tests.
func buildSkillIndex(ctx context.Context, dispatch verbDispatch, scope, wsID, pjID string) ([]skillIndexEntry, error) {
	subs, err := listSubstrateSkills(ctx, dispatch, scope, wsID, pjID, true /* effective: user overrides win in the index */)
	if err != nil {
		return nil, err
	}
	out := make([]skillIndexEntry, 0, len(subs))
	for _, s := range subs {
		e, ferr := skillToIndexEntry(s)
		if ferr != nil {
			return nil, ferr
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// skillToIndexEntry projects one substrate skill's frontmatter into a dispatch
// entry (body excluded).
func skillToIndexEntry(s substrateSkill) (skillIndexEntry, error) {
	fm, _, ferr := frontmatter.Parse([]byte(s.Body))
	if ferr != nil {
		return skillIndexEntry{}, fmt.Errorf("skill index: parse frontmatter for %q: %w", s.Name, ferr)
	}
	return skillIndexEntry{
		Name:        s.Name,
		Kind:        strings.TrimSpace(fm.Kind),
		AppliesTo:   fm.AppliesTo,
		When:        strings.TrimSpace(fm.When),
		Description: strings.TrimSpace(fm.Description),
		LocalName:   localSkillName(s.Name),
	}, nil
}

func newSkillIndexCmd(configArg, userArg *string) *cobra.Command {
	var (
		scopeArg string
		wsArg    string
		pjArg    string
	)
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Print the dispatch index (name/kind/applies_to/when/description; bodies excluded)",
		Long: `index projects every type:skill row's frontmatter into one compact
line per skill — name, kind, applies_to, when, description — bodies excluded.
It is the context-safe dynamic index the agent dispatches off: match a story's
type + status to a skill, then load only that skill's body. applies_to is the
single source for which workflow a story type uses (no project-config).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			dispatch := func(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
				return dispatchVerb(ctx, name, req, *configArg, *userArg)
			}
			index, err := buildSkillIndex(ctx, dispatch, scopeArg, wsArg, pjArg)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, e := range index {
				applies := "-"
				if len(e.AppliesTo) > 0 {
					applies = strings.Join(e.AppliesTo, ",")
				}
				when := e.When
				if when == "" {
					when = "-"
				}
				kind := e.Kind
				if kind == "" {
					kind = "?"
				}
				fmt.Fprintf(out, "%-32s %-10s applies_to=%-28s when=%-20s %s\n",
					e.Name, kind, applies, when, e.Description)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&scopeArg, "scope", "project", "Scope to index (system / workspace / project)")
	cmd.Flags().StringVar(&wsArg, "workspace", "", "workspace_id (required for scope=workspace or project)")
	cmd.Flags().StringVar(&pjArg, "project", "", "project_id (required for scope=project)")
	return cmd
}
