// Shared scaffolding for the per-noun list/get CLI subcommands.
// `satellites {skill,principle,document} list` and `… get` all reduce
// to a `document_list` / `document_get` dispatch with the noun's type
// / tag filter applied. This file holds the helper that builds each
// command so the per-noun command files stay one-liners.
//
// No new MCP verbs are added — every command here is a client-side
// composition of the existing document_* surface. The pre-existing
// `no-new-mcp-verbs` project principle is the reason: nouns are CLI
// ergonomics, not server-side discriminators.
//
// The CLI must not import internal/document (transport layering
// guard); responses are decoded into local view structs that mirror
// only the fields the CLI renders.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/spf13/cobra"
)

// substrateNounConfig describes one noun's list/get command shape.
// Exactly one of FilterType / FilterTagPrefix is set — list/get
// commands route their filter onto the document_list / document_get
// request based on which is present.
type substrateNounConfig struct {
	Use             string  // cobra Use string, e.g. "list" or "get"
	Short           string  // one-line description for --help
	FilterType      string  // "skill" / "document" — empty when filtering by tag prefix
	FilterTagPrefix string  // e.g. "principles:" — empty when filtering by type
	ConfigArg       *string // --config flag value (shared at parent level)
	UserArg         *string // --user flag value (shared at parent level)
}

// docListView is the shape decoded from a document_list response. It
// mirrors the subset of internal/document.Document the CLI renders;
// keeping it local avoids the transport layering guard that forbids
// internal/cli from importing internal/document.
type docListView struct {
	Items []nounListItem `json:"items"`
}

// nounListItem is the projected wire shape rendered by `… list`.
type nounListItem struct {
	Name          string    `json:"name"`
	Scope         string    `json:"scope"`
	Type          string    `json:"type"`
	LatestVersion int       `json:"latest_version"`
	UpdatedAt     time.Time `json:"updated_at"`
	Tags          []string  `json:"tags"`
}

// docGetView mirrors internal/verb.DocumentGetResponse.RenderedBody.
type docGetView struct {
	RenderedBody string `json:"rendered_body"`
}

// docListRequest is the JSON-only request the CLI sends for
// document_list. Field names track internal/verb.DocumentListRequest;
// the local declaration keeps the import surface narrow.
type docListRequest struct {
	Type        string   `json:"type,omitempty"`
	Scope       string   `json:"scope,omitempty"`
	WorkspaceID string   `json:"workspace_id,omitempty"`
	ProjectID   string   `json:"project_id,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	// Effective overlays the caller's user-scope overrides onto the listed
	// set (sty_cbeeb452) — the skill index uses it so a user's overridden
	// workflow/gate skill resolves for that user.
	Effective bool `json:"effective,omitempty"`
}

// newSubstrateListCmd builds a `<noun> list` cobra command.
func newSubstrateListCmd(cfg substrateNounConfig) *cobra.Command {
	var (
		scopeArg string
		wsArg    string
		pjArg    string
	)
	cmd := &cobra.Command{
		Use:   cfg.Use,
		Short: cfg.Short,
		Long:  cfg.Short + "\n\nThin shell over the document_list MCP verb.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			// Default scope: with NO scope/workspace/project flag, list the
			// caller's EFFECTIVE set — system + the configured project — not an
			// unscoped all-projects list (which leaks other projects' rows into
			// a configured repo; sty_de7e2008). An explicit --scope keeps the
			// single-scope behaviour. Only engages when a project is configured;
			// an unconfigured caller falls back to the unscoped list.
			if scopeArg == "" && wsArg == "" && pjArg == "" {
				if items, ok, err := callerScopedList(ctx, cfg); err != nil {
					return err
				} else if ok {
					renderNounList(cmd.OutOrStdout(), filterByTagPrefix(items, cfg.FilterTagPrefix))
					return nil
				}
			}

			req := docListRequest{
				Type:        cfg.FilterType,
				Scope:       scopeArg,
				WorkspaceID: wsArg,
				ProjectID:   pjArg,
				Limit:       200,
			}
			if cfg.FilterTagPrefix != "" && scopeArg != "" {
				// Substrate tag-contains is AND across the slice; a scoped
				// principles listing maps to the matching tag value.
				req.Tags = []string{cfg.FilterTagPrefix + scopeArg}
			}
			raw, err := json.Marshal(req)
			if err != nil {
				return err
			}
			resp, err := dispatchVerb(ctx, "document_list", raw, *cfg.ConfigArg, *cfg.UserArg)
			if err != nil {
				return err
			}
			var parsed docListView
			if err := json.Unmarshal(resp, &parsed); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			items := filterByTagPrefix(parsed.Items, cfg.FilterTagPrefix)
			renderNounList(cmd.OutOrStdout(), items)
			return nil
		},
	}
	cmd.Flags().StringVar(&scopeArg, "scope", "", "Filter by scope (system / workspace / project)")
	cmd.Flags().StringVar(&wsArg, "workspace", "", "Filter by workspace_id (required when scope=workspace or scope=project)")
	cmd.Flags().StringVar(&pjArg, "project", "", "Filter by project_id (required when scope=project)")
	return cmd
}

// newSubstrateGetCmd builds a `<noun> get <name>` cobra command.
func newSubstrateGetCmd(cfg substrateNounConfig) *cobra.Command {
	var (
		scopeArg string
		wsArg    string
		pjArg    string
	)
	cmd := &cobra.Command{
		Use:   cfg.Use + " <name>",
		Short: cfg.Short,
		Long:  cfg.Short + "\n\nThin shell over the document_get MCP verb.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := struct {
				Name        string `json:"name"`
				Scope       string `json:"scope,omitempty"`
				WorkspaceID string `json:"workspace_id,omitempty"`
				ProjectID   string `json:"project_id,omitempty"`
			}{
				Name:        args[0],
				Scope:       scopeArg,
				WorkspaceID: wsArg,
				ProjectID:   pjArg,
			}
			raw, err := json.Marshal(req)
			if err != nil {
				return err
			}
			resp, err := dispatchVerb(context.Background(), "document_get", raw, *cfg.ConfigArg, *cfg.UserArg)
			if err != nil {
				return err
			}
			var parsed docGetView
			if err := json.Unmarshal(resp, &parsed); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			fmt.Fprint(cmd.OutOrStdout(), parsed.RenderedBody)
			if parsed.RenderedBody != "" && !strings.HasSuffix(parsed.RenderedBody, "\n") {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&scopeArg, "scope", "", "Document scope (system / workspace / project; defaults to system)")
	cmd.Flags().StringVar(&wsArg, "workspace", "", "workspace_id (required when scope=workspace or scope=project)")
	cmd.Flags().StringVar(&pjArg, "project", "", "project_id (required when scope=project)")
	return cmd
}

// callerScopedList lists a noun's EFFECTIVE set for a configured caller —
// system-scope rows plus the configured project's rows — so a default
// (unflagged) list never surfaces another project's rows (sty_de7e2008). It
// engages only for the type-filtered nouns (skill / document), not the
// tag-filtered principle noun, and only when a project is configured. Returns
// ok=false to signal the caller to fall back to the plain unscoped list rather
// than fail (an unconfigured or unresolvable caller still gets a listing).
func callerScopedList(ctx context.Context, cfg substrateNounConfig) ([]nounListItem, bool, error) {
	if cfg.FilterTagPrefix != "" {
		return nil, false, nil // principle listing keeps its tag-scoped behaviour
	}
	conf, _, err := cliconfig.Load(*cfg.ConfigArg)
	if err != nil || strings.TrimSpace(conf.ProjectID) == "" {
		return nil, false, nil // unconfigured — fall back to the unscoped list
	}
	projectID := strings.TrimSpace(conf.ProjectID)
	wsID, err := listResolveWorkspaceID(ctx, cfg, projectID)
	if err != nil {
		return nil, false, nil // cannot resolve — fall back rather than fail
	}
	list := func(req docListRequest) ([]nounListItem, error) {
		raw, mErr := json.Marshal(req)
		if mErr != nil {
			return nil, mErr
		}
		resp, dErr := dispatchVerb(ctx, "document_list", raw, *cfg.ConfigArg, *cfg.UserArg)
		if dErr != nil {
			return nil, dErr
		}
		var parsed docListView
		if uErr := json.Unmarshal(resp, &parsed); uErr != nil {
			return nil, fmt.Errorf("decode response: %w", uErr)
		}
		return parsed.Items, nil
	}
	sys, err := list(docListRequest{Type: cfg.FilterType, Scope: "system", Limit: 200})
	if err != nil {
		return nil, false, err
	}
	proj, err := list(docListRequest{Type: cfg.FilterType, Scope: "project", WorkspaceID: wsID, ProjectID: projectID, Limit: 200})
	if err != nil {
		return nil, false, err
	}
	return append(sys, proj...), true, nil
}

// listResolveWorkspaceID resolves a project's workspace_id via project_get,
// decoding only the one field (layering guard: no internal/project import).
func listResolveWorkspaceID(ctx context.Context, cfg substrateNounConfig, projectID string) (string, error) {
	raw, err := json.Marshal(map[string]string{"id": projectID})
	if err != nil {
		return "", err
	}
	resp, err := dispatchVerb(ctx, "project_get", raw, *cfg.ConfigArg, *cfg.UserArg)
	if err != nil {
		return "", err
	}
	var got struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(resp, &got); err != nil {
		return "", err
	}
	if strings.TrimSpace(got.WorkspaceID) == "" {
		return "", fmt.Errorf("project %s returned no workspace_id", projectID)
	}
	return got.WorkspaceID, nil
}

// filterByTagPrefix narrows a list result to items whose tags contain
// at least one tag with the supplied prefix. Empty prefix is a pass-
// through. Used to post-filter the principles listing because the
// substrate's tag-contains semantics are AND-equality across the slice
// — a "list every row tagged principles:*" can't be a single server-
// side filter.
func filterByTagPrefix(items []nounListItem, tagPrefix string) []nounListItem {
	if tagPrefix == "" {
		return items
	}
	out := make([]nounListItem, 0, len(items))
	for _, it := range items {
		if hasTagWithPrefix(it.Tags, tagPrefix) {
			out = append(out, it)
		}
	}
	return out
}

func hasTagWithPrefix(tags []string, prefix string) bool {
	for _, t := range tags {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

// renderNounList prints a compact table: NAME, SCOPE, VERSION, UPDATED.
func renderNounList(out interface{ Write(p []byte) (int, error) }, items []nounListItem) {
	if len(items) == 0 {
		fmt.Fprintln(out, "(no rows)")
		return
	}
	const layout = "2006-01-02 15:04 UTC"
	fmt.Fprintf(out, "%-40s  %-10s  %7s  %s\n", "NAME", "SCOPE", "VERSION", "UPDATED")
	for _, it := range items {
		fmt.Fprintf(out, "%-40s  %-10s  %7d  %s\n", truncate(it.Name, 40), it.Scope, it.LatestVersion, it.UpdatedAt.UTC().Format(layout))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
