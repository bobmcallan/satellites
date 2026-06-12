// `satellites story get` — the server-side read half of "what's happening":
// one story's status, state actor, and identity fields without an MCP
// round-trip (sty_eb4876e9). The local, live-engagement half is
// `satellites work status`; this command is its server complement and reads
// only — it never writes or transitions.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/spf13/cobra"
)

func newStoryGetCmd(configArg, userArg *string) *cobra.Command {
	return &cobra.Command{
		Use:   "get <story-id>",
		Short: "Read a story's server-side state: status, state actor, category, priority, tags, parent, headline",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStoryGet(cmd.Context(), cmd.OutOrStdout(), *configArg, *userArg, args[0])
		},
	}
}

func runStoryGet(ctx context.Context, out io.Writer, configPath, userArg, storyID string) error {
	req, err := json.Marshal(verb.DocumentGetRequest{ID: storyID})
	if err != nil {
		return err
	}
	raw, err := dispatchVerb(ctx, "document_get", req, configPath, userArg)
	if err != nil {
		return fmt.Errorf("story get: resolve %s: %w", storyID, err)
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("story get: decode %s: %w", storyID, err)
	}
	if resp.Document.Type != "story" {
		return fmt.Errorf("story get: %s is type=%q, not a story", storyID, resp.Document.Type)
	}
	body := resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[len(resp.Versions)-1].Body
	}
	fmt.Fprint(out, formatStoryGet(resp, body))
	return nil
}

// formatStoryGet renders the story view — pure for tests. The state actor
// comes from the story's own embedded ## Workflow; a story without one (or a
// status its workflow doesn't declare an actor for) shows "-".
func formatStoryGet(resp verb.DocumentGetResponse, body string) string {
	d := resp.Document
	actor := "-"
	if wf, err := workflow.ParseBody([]byte(body)); err == nil && wf != nil {
		if st, ok := wf.StateOf(d.Status); ok && strings.TrimSpace(st.Actor) != "" {
			actor = st.Actor
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n", d.ID, d.Name)
	fmt.Fprintf(&b, "status:    %s (actor: %s)\n", d.Status, actor)
	fmt.Fprintf(&b, "category:  %s\n", dashIfEmpty(d.Category))
	fmt.Fprintf(&b, "priority:  %s\n", dashIfEmpty(d.Priority))
	fmt.Fprintf(&b, "tags:      %s\n", dashIfEmpty(strings.Join(d.Tags, ", ")))
	fmt.Fprintf(&b, "parent:    %s\n", dashIfEmpty(d.ParentID))
	fmt.Fprintf(&b, "headline:  %s\n", dashIfEmpty(d.Headline))
	return b.String()
}

// dashIfEmpty keeps empty optional fields visibly empty rather than blank.
func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
