// `satellites document index` — the context-cheap discovery listing for
// documents and principles (epic:always-context, sty_285292a6).
//
// One line per type:document row (principles are type:document rows tagged
// principles:*) across the effective scopes: name, scope, the always-residency
// flag (the principles:always tag), and the caveman headline. Bodies are never
// loaded — the listing is what an agent consults to decide which non-resident
// document/principle to lazy-load, the read side of the always-context model.
//
// Skills are excluded (their description is already surfaced by the harness).
// Composes the existing document_list verb — no new MCP verb (no-new-mcp-verbs).

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// alwaysTag marks a document/principle as resident — hard-injected at session
// boundaries rather than discovered on demand. It generalises the existing
// principles:always tag (carried by agent-goals, broken-windows,
// story-execution-process) to any document.
const alwaysTag = "principles:always"

// documentIndexCap bounds the listing so a project with a very large document
// set cannot blow the context budget the index is meant to protect. When the
// substrate holds more rows than this, the listing is truncated and the drop is
// reported on stderr — never silently (epic:always-context AC4).
const documentIndexCap = 500

// documentIndexEntry is one document/principle's discovery projection: identity
// + residency + headline, never its body.
type documentIndexEntry struct {
	Name     string
	Scope    string
	Always   bool
	Headline string
}

// buildDocumentIndex lists the effective type:document set for the scope and
// projects each row into a discovery entry. Returns the entries plus a
// truncated flag set when the substrate held more rows than the cap.
func buildDocumentIndex(ctx context.Context, dispatch verbDispatch, scope, wsID, pjID string) ([]documentIndexEntry, bool, error) {
	// Request one past the cap so a full page tells us more rows exist.
	listReq, err := json.Marshal(docListRequest{
		Type:        "document",
		Scope:       scope,
		WorkspaceID: wsID,
		ProjectID:   pjID,
		Limit:       documentIndexCap + 1,
		Effective:   true,
	})
	if err != nil {
		return nil, false, err
	}
	raw, err := dispatch(ctx, "document_list", listReq)
	if err != nil {
		return nil, false, fmt.Errorf("document index: list: %w", err)
	}
	var listed struct {
		Items []struct {
			Name     string   `json:"name"`
			Scope    string   `json:"scope"`
			Tags     []string `json:"tags"`
			Headline string   `json:"headline"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, false, fmt.Errorf("document index: decode list: %w", err)
	}

	truncated := false
	items := listed.Items
	if len(items) > documentIndexCap {
		truncated = true
		items = items[:documentIndexCap]
	}

	out := make([]documentIndexEntry, 0, len(items))
	for _, it := range items {
		out = append(out, documentIndexEntry{
			Name:     it.Name,
			Scope:    it.Scope,
			Always:   hasTag(it.Tags, alwaysTag),
			Headline: strings.TrimSpace(it.Headline),
		})
	}
	// Deterministic order: resident first, then by name — the agent reads the
	// always set at the top, the discoverable rest below.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Always != out[j].Always {
			return out[i].Always
		}
		return out[i].Name < out[j].Name
	})
	return out, truncated, nil
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func newDocumentIndexCmd(configArg, userArg *string) *cobra.Command {
	var (
		scopeArg string
		wsArg    string
		pjArg    string
	)
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Print the document/principle discovery index (name/scope/always/headline; bodies excluded)",
		Long: `index lists one line per document/principle across the effective scopes —
name, scope, the always-residency flag, and the caveman headline — bodies
excluded. It is the context-cheap listing an agent consults to discover which
non-resident document or principle to lazy-load. Skills are excluded. The
listing is bounded by a hard cap; truncation is reported on stderr.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			dispatch := func(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
				return dispatchVerb(ctx, name, req, *configArg, *userArg)
			}
			// Bind project/workspace from --config when not passed, mirroring
			// `skill index` — a bare invocation otherwise leaves project scope
			// unbound and the server rejects it. Explicit flags win. System
			// scope takes no binding: system rows carry no workspace/project,
			// and the store ANDs every supplied id, so a bound system query
			// matches nothing.
			if scopeArg != "system" {
				wsArg, pjArg = resolveSkillScopeBinding(ctx, *configArg, *userArg, wsArg, pjArg)
			}
			index, truncated, err := buildDocumentIndex(ctx, dispatch, scopeArg, wsArg, pjArg)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, e := range index {
				always := "-"
				if e.Always {
					always = "always"
				}
				headline := e.Headline
				if headline == "" {
					headline = "-"
				}
				fmt.Fprintf(out, "%-40s %-10s %-8s %s\n", e.Name, e.Scope, always, headline)
			}
			if truncated {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"document index: truncated to %d rows — more documents exist; narrow by scope to see the rest\n",
					documentIndexCap)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&scopeArg, "scope", "project", "Scope to index (system / workspace / project)")
	cmd.Flags().StringVar(&wsArg, "workspace", "", "workspace_id (required for scope=workspace or project)")
	cmd.Flags().StringVar(&pjArg, "project", "", "project_id (required for scope=project)")
	return cmd
}
