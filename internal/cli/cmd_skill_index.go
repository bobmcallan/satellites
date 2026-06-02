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
	"path/filepath"
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

// buildEffectiveSkillIndex builds the dispatch index a project actually
// resolves against: the scope cascade system → workspace → project, merged by
// name with the higher scope winning (project overrides workspace overrides
// system). This is what makes a baseline workflow + gate skills seeded at
// system scope run for a project with zero project-scoped skills, while a
// project still overrides any baseline by authoring a same-named skill
// (sty_050b6653). The project layer is resolved effectively so a caller's
// user-scope override wins at the top (sty_cbeeb452). No new MCP verb — it
// composes listSubstrateSkills across scopes.
func buildEffectiveSkillIndex(ctx context.Context, dispatch verbDispatch, wsID, pjID string) ([]skillIndexEntry, error) {
	byName := map[string]skillIndexEntry{}
	merge := func(scope, ws, pj string, effective bool) error {
		subs, err := listSubstrateSkills(ctx, dispatch, scope, ws, pj, effective)
		if err != nil {
			return err
		}
		for _, s := range subs {
			e, ferr := skillToIndexEntry(s)
			if ferr != nil {
				return ferr
			}
			byName[e.Name] = e // later (higher-precedence) scope overrides
		}
		return nil
	}
	// Lowest precedence first.
	if err := merge("system", "", "", false); err != nil {
		return nil, err
	}
	if strings.TrimSpace(wsID) != "" {
		if err := merge("workspace", wsID, "", false); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(pjID) != "" {
		if err := merge("project", wsID, pjID, true); err != nil {
			return nil, err
		}
	}
	out := make([]skillIndexEntry, 0, len(byName))
	for _, e := range byName {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// selectWorkflowSkill returns the materialised local path of the workflow
// skill bound to storyType — the kind:workflow entry whose applies_to contains
// it. Index-derived dispatch: applies_to is the single source, no
// project-config story_types (sty_815c09e7).
func selectWorkflowSkill(index []skillIndexEntry, storyType string) (string, error) {
	for _, e := range index {
		if e.Kind != "workflow" {
			continue
		}
		for _, t := range e.AppliesTo {
			if strings.TrimSpace(t) == storyType {
				return filepath.Join(".claude", "skills", e.LocalName, "SKILL.md"), nil
			}
		}
	}
	return "", fmt.Errorf("no kind:workflow skill has applies_to %q in the skill index", storyType)
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
