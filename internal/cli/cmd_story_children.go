// `satellites story children <anchor-id>` — enumerate an anchor (parent/epic)
// story's children and their statuses. This is the deterministic functional half
// of satellites-parent-close-review (sty_b86a94c7): the reviewer's `check:` runs
// it (over $SATELLITES_STORY_ID, injected by the gate harness) and judges that
// every child has reached a terminal status before enacting backlog → done.
//
// It reuses the existing document_list verb (which returns parent_id + status per
// row) and filters by parent_id — no new server surface. Exit 0 when every child
// is terminal (or there are none); exit 1 when any child is non-terminal, so it
// also stands alone as a deterministic close check.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// childRow is the minimal projection of a child story the close check reads.
type childRow struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Parent string `json:"parent_id"`
}

func newStoryChildrenCmd(configArg, userArg *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "children <anchor-id>",
		Short: "List an anchor story's children and their statuses — exit 1 if any child is non-terminal",
		Long: `children enumerates every story whose parent_id is the given anchor and
reports each one's status, marking which have reached a terminal status
(done / cancelled / deleted). It is the functional check the
satellites-parent-close-review reviewer reads to judge an epic's close
contract ("every child is terminal"). Exit 0 when all children are terminal
(or there are none); exit 1 when any is non-terminal.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			dispatch := func(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
				return dispatchVerb(ctx, name, req, *configArg, *userArg)
			}
			return runStoryChildren(ctx, cmd.OutOrStdout(), dispatch, args[0])
		},
	}
	return cmd
}

// runStoryChildren lists the anchor's children and prints the close summary.
//
// Children are enumerated in the ANCHOR's own project — resolved from the anchor
// id, not the caller's config project (sty_e383c48b). Run from a repo bound to a
// different project, scoping to the config project returned an empty set and a
// false "every child is terminal — the anchor may close"; the parent-close gate
// reads this check, so a gate run from the wrong worktree could vacuously accept
// a close. Resolving the anchor's project (and failing loudly when the id does
// not resolve to a story) makes the close contract independent of cwd.
func runStoryChildren(ctx context.Context, out io.Writer, dispatch verbDispatch, anchorID string) error {
	getReq, err := json.Marshal(verb.DocumentGetRequest{ID: anchorID})
	if err != nil {
		return err
	}
	graw, err := dispatch(ctx, "document_get", getReq)
	if err != nil {
		return fmt.Errorf("story children: resolve anchor %s: %w", anchorID, err)
	}
	var anchorResp verb.DocumentGetResponse
	if err := json.Unmarshal(graw, &anchorResp); err != nil {
		return fmt.Errorf("story children: decode anchor %s: %w", anchorID, err)
	}
	if anchorResp.Document.Type != "story" {
		return fmt.Errorf("story children: anchor %s is type=%q, not a story", anchorID, anchorResp.Document.Type)
	}
	projectID := anchorResp.Document.ProjectID
	if strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("story children: anchor %s has no project — cannot enumerate children", anchorID)
	}
	listReq, err := json.Marshal(docListRequest{Type: "story", ProjectID: projectID, Limit: 500})
	if err != nil {
		return err
	}
	raw, err := dispatch(ctx, "document_list", listReq)
	if err != nil {
		return fmt.Errorf("story children: list: %w", err)
	}
	var listed struct {
		Items []childRow `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return fmt.Errorf("story children: decode list: %w", err)
	}

	var children []childRow
	for _, it := range listed.Items {
		if it.Parent == anchorID {
			children = append(children, it)
		}
	}
	sort.Slice(children, func(i, j int) bool { return children[i].ID < children[j].ID })

	var nonTerminal []string
	for _, c := range children {
		terminal := terminalStoryStatuses[strings.ToLower(strings.TrimSpace(c.Status))]
		mark := "non-terminal"
		if terminal {
			mark = "terminal"
		} else {
			nonTerminal = append(nonTerminal, c.ID)
		}
		fmt.Fprintf(out, "%-12s %-12s %s\n", c.ID, c.Status, mark)
	}
	fmt.Fprintf(out, "\nanchor %s: %d child(ren), %d non-terminal\n", anchorID, len(children), len(nonTerminal))
	if len(nonTerminal) > 0 {
		fmt.Fprintf(out, "non-terminal: %v\n", nonTerminal)
		return fmt.Errorf("story children: %d non-terminal child(ren) — the anchor cannot close", len(nonTerminal))
	}
	fmt.Fprintln(out, "every child is terminal — the anchor may close")
	return nil
}
