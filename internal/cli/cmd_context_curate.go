// `satellites context curate <story>` — operator curation of the delivered
// context, by options (epic:qa-observability, story:sty_1b44aa75). Once the
// context is visible (order:2) and measured, the operator should be able to
// trim it against the budget so "keep context flat / trend down" is an action,
// not a hope.
//
// The one genuinely operator-curatable, CONFIG-driven knob among the always-on
// components is the principles ride-along: membership is the `principles:always`
// tag (data, per internal/verb/principles.go), not code. Load-context, MCP tool
// descriptions, and the skills index are fixed by the embed / materialisation
// and cannot be trimmed by config without breaking gates — so this command
// curates the ride-along and names the rest as fixed.
//
//	list (default / --json) — the ride-along principles + sizes + always-on total
//	--drop NAME            — remove principles:always (shrink the next assembly)
//	--restore NAME         — re-add it (undo; never grows beyond the natural set)
//
// Config-driven (a tag), shrinks-only, measurable (always-on size before→after,
// reusing order:2's assembly), operator-facing + out of band, no new MCP verb.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

func newContextCurateCmd(configArg, userArg *string) *cobra.Command {
	var (
		asJSON  bool
		drop    string
		restore string
	)
	cmd := &cobra.Command{
		Use:   "curate <story-id>",
		Short: "Curate the delivered context — trim the principles ride-along by option",
		Long: `curate trims what the substrate delivers to the agent, against the budget.

With no flag it lists the principles ride-along (the config-driven, operator-
curatable component) with per-principle size and the always-on total. --drop NAME
removes principles:always from a principle so the next assembly delivers less;
--restore NAME undoes it. The always-on size is printed before→after, reusing the
order:2 assembly, so curation is measurable. Load-context / tool descriptions /
skills index are fixed (not curatable here). Operator-facing, out of band.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			storyID := args[0]
			if drop != "" && restore != "" {
				return fmt.Errorf("pass at most one of --drop / --restore")
			}
			switch {
			case drop != "":
				return curateToggle(ctx, storyID, drop, false, *configArg, *userArg, cmd.OutOrStdout())
			case restore != "":
				return curateToggle(ctx, storyID, restore, true, *configArg, *userArg, cmd.OutOrStdout())
			default:
				return curateList(ctx, storyID, asJSON, *configArg, *userArg, cmd.OutOrStdout())
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the ride-along list as JSON.")
	cmd.Flags().StringVar(&drop, "drop", "", "Remove principles:always from the named principle (shrink the delivered context).")
	cmd.Flags().StringVar(&restore, "restore", "", "Re-add principles:always to the named principle (undo a drop).")
	return cmd
}

func curateList(ctx context.Context, storyID string, asJSON bool, configPath, userArg string, w io.Writer) error {
	story, _, err := reviewGetStory(ctx, reviewOpts{StoryID: storyID, ConfigPath: configPath, UserArg: userArg})
	if err != nil {
		return err
	}
	ps := ridealongPrinciples(ctx, story, configPath, userArg)
	always, _ := alwaysOnTotal(ctx, storyID, configPath, userArg)
	trimmable := trimmableBytes(ps)
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(buildCurateJSON(storyID, ps, always))
	}
	if len(ps) == 0 {
		fmt.Fprintf(w, "curate %s: ride-along is empty (0 principles tagged %s) — nothing to trim.\n", storyID, verb.PrincipleTagAlways)
		fmt.Fprintf(w, "always-on delivered context: %d bytes (load-context + tool descriptions + skills index are fixed, not curatable here).\n", always)
		return nil
	}
	fmt.Fprintf(w, "curate %s: %d principle(s) in the ride-along (curatable via --drop)\n\n", storyID, len(ps))
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PRINCIPLE\tSCOPE\tBYTES")
	for _, p := range ps {
		fmt.Fprintf(tw, "%s\t%s\t%d\n", p.Name, p.Scope, p.Bytes)
	}
	fmt.Fprintf(tw, "  trimmable subtotal\t\t%d\n", trimmable)
	fmt.Fprintf(tw, "  always-on total\t\t%d\n", always)
	tw.Flush()
	fmt.Fprintf(w, "\ntrim: satellites context curate %s --drop <PRINCIPLE>\n", storyID)
	return nil
}

func curateToggle(ctx context.Context, storyID, name string, include bool, configPath, userArg string, w io.Writer) error {
	story, _, err := reviewGetStory(ctx, reviewOpts{StoryID: storyID, ConfigPath: configPath, UserArg: userArg})
	if err != nil {
		return err
	}
	before, _ := alwaysOnTotal(ctx, storyID, configPath, userArg)

	p, ok := findPrincipleByName(ctx, story, name, configPath, userArg)
	if !ok {
		return fmt.Errorf("principle %q not found in any scope for this story", name)
	}
	if err := setPrincipleAlways(ctx, p, include, configPath, userArg); err != nil {
		return fmt.Errorf("update %q tags: %w", name, err)
	}
	after, _ := alwaysOnTotal(ctx, storyID, configPath, userArg)

	verbWord := "dropped from"
	if include {
		verbWord = "restored to"
	}
	fmt.Fprintf(w, "%s %s ride-along (%s, scope=%s)\n", name, verbWord, p.ID, p.Scope)
	fmt.Fprintf(w, "always-on delivered context: %d → %d bytes (%+d)\n", before, after, after-before)
	return nil
}

// trimmableBytes is the curation-trimmable subtotal — the bytes the ride-along
// contributes to the always-on surface (what --drop can remove). Pure.
func trimmableBytes(ps []ridealongPrincipleInfo) int {
	n := 0
	for _, p := range ps {
		n += p.Bytes
	}
	return n
}

// buildCurateJSON is the --json payload for the ride-along list. Pure over its
// inputs so the shape is unit-testable without dispatch.
func buildCurateJSON(storyID string, ps []ridealongPrincipleInfo, always int) map[string]any {
	return map[string]any{
		"story_id":         storyID,
		"ridealong":        ps,
		"always_on_bytes":  always,
		"trimmable_bytes":  trimmableBytes(ps),
		"fixed_components": []string{"load-context (MCP instructions)", "MCP tool descriptions", "skills index"},
	}
}

// alwaysOnTotal recomputes the always-on delivered size via order:2's assembly —
// the single measure source, so curation's before/after ties to the budget.
func alwaysOnTotal(ctx context.Context, storyID, configPath, userArg string) (int, error) {
	dc, err := assembleDeliveredContext(ctx, storyID, configPath, userArg)
	if err != nil {
		return 0, err
	}
	return dc.TotalsBytes[partAlwaysOn], nil
}

// principleDoc is a principle document resolved for a tag toggle — enough to
// read-modify-write its tags without clobbering its body.
type principleDoc struct {
	ID          string
	Name        string
	Scope       string // system | workspace | project
	WorkspaceID string
	ProjectID   string
	Tags        []string
	Body        string
}

// findPrincipleByName resolves a principle by name across the scopes that apply
// to the story (system → workspace → project), returning its full doc so the
// caller can rewrite tags while preserving the body.
func findPrincipleByName(ctx context.Context, story reviewStory, name, configPath, userArg string) (principleDoc, bool) {
	reqs := []struct {
		ps    verb.PrincipleScope
		scope string
		ws    string
		pj    string
	}{
		{verb.PrincipleScopeGlobal, "system", "", ""},
		{verb.PrincipleScopeWorkspace, "workspace", story.WorkspaceID, ""},
		{verb.PrincipleScopeProject, "project", story.WorkspaceID, story.ProjectID},
	}
	for _, r := range reqs {
		if r.scope == "workspace" && r.ws == "" {
			continue
		}
		if r.scope == "project" && (r.ws == "" || r.pj == "") {
			continue
		}
		listReq, err := json.Marshal(verb.DocumentListRequest{
			Type:        "document",
			Scope:       r.scope,
			WorkspaceID: r.ws,
			ProjectID:   r.pj,
			Tags:        []string{verb.PrincipleTag(r.ps)},
			Status:      "active",
			Limit:       200,
		})
		if err != nil {
			continue
		}
		raw, err := dispatchVerb(ctx, "document_list", listReq, configPath, userArg)
		if err != nil {
			continue
		}
		var lr verb.DocumentListResponse
		if err := json.Unmarshal(raw, &lr); err != nil {
			continue
		}
		for _, item := range lr.Items {
			if item.Name != name {
				continue
			}
			gReq, err := json.Marshal(verb.DocumentGetRequest{ID: item.ID})
			if err != nil {
				continue
			}
			graw, err := dispatchVerb(ctx, "document_get", gReq, configPath, userArg)
			if err != nil {
				continue
			}
			var gr verb.DocumentGetResponse
			if err := json.Unmarshal(graw, &gr); err != nil {
				continue
			}
			body := gr.RawBody
			if body == "" && len(gr.Versions) > 0 {
				body = gr.Versions[0].Body
			}
			return principleDoc{
				ID:          item.ID,
				Name:        item.Name,
				Scope:       r.scope,
				WorkspaceID: r.ws,
				ProjectID:   r.pj,
				Tags:        gr.Document.Tags,
				Body:        body,
			}, true
		}
	}
	return principleDoc{}, false
}

// setPrincipleAlways rewrites a principle's tags to include or exclude
// principles:always, preserving its body (a key-addressed document_upsert).
func setPrincipleAlways(ctx context.Context, p principleDoc, include bool, configPath, userArg string) error {
	var newTags []string
	if include {
		newTags = withTag(p.Tags, verb.PrincipleTagAlways)
	} else {
		newTags = withoutTag(p.Tags, verb.PrincipleTagAlways)
	}
	req, err := json.Marshal(verb.DocumentUpsertRequest{
		Type:        "document",
		Scope:       p.Scope,
		Name:        p.Name,
		WorkspaceID: p.WorkspaceID,
		ProjectID:   p.ProjectID,
		Body:        p.Body,
		Tags:        &newTags,
	})
	if err != nil {
		return err
	}
	_, err = dispatchVerb(ctx, "document_upsert", req, configPath, userArg)
	return err
}

// withoutTag returns tags with tag removed (all occurrences). Pure.
func withoutTag(tags []string, tag string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t != tag {
			out = append(out, t)
		}
	}
	return out
}

// withTag returns tags with tag appended if absent. Pure; never duplicates.
func withTag(tags []string, tag string) []string {
	for _, t := range tags {
		if t == tag {
			return append([]string(nil), tags...)
		}
	}
	return append(append([]string(nil), tags...), tag)
}
