// `satellites skill search` and `satellites skill adopt` — the discovery
// and fork-and-own halves of library consumption (epic:skill-library,
// sty_59ad5c0d).
//
// search lists matching skills across the shared library (every publisher)
// plus the caller's accessible scopes, one row per (publisher, name) so
// competing offerings compare at a glance. adopt copies one library skill
// into .satellites/skills/ with a forked-from provenance stamp in its
// frontmatter — from then on it is an ordinary project skill on the normal
// upload/review path, with no upstream link. Both are client-side
// compositions of document_list / document_get; no new MCP verbs.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/spf13/cobra"
)

// librarySearchHit is one rendered search row.
type librarySearchHit struct {
	Name      string
	Publisher string // project_id for library rows; "-" otherwise
	Scope     string
	Version   int
	Headline  string
}

// searchListItem is the document_list projection search needs (the noun
// list view lacks publisher + headline).
type searchListItem struct {
	Name          string `json:"name"`
	Scope         string `json:"scope"`
	WorkspaceID   string `json:"workspace_id"`
	ProjectID     string `json:"project_id"`
	LatestVersion int    `json:"latest_version"`
	Headline      string `json:"headline"`
}

// newSkillSearchCmd builds the `skill search <term>` command.
func newSkillSearchCmd(configArg, userArg *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <term>",
		Short: "Search skills by name/description across the shared library and the caller's accessible scopes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			dispatch := func(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
				return dispatchVerb(ctx, name, req, *configArg, *userArg)
			}
			hits, err := searchSkills(ctx, dispatch, *configArg, *userArg, args[0])
			if err != nil {
				return err
			}
			renderSearchHits(cmd.OutOrStdout(), hits)
			return nil
		},
	}
	return cmd
}

// searchSkills lists skill rows per searched scope — the library always,
// plus system and the configured repo's workspace/project — fetches each
// candidate's body, and matches term case-insensitively against the name
// and the frontmatter description.
func searchSkills(ctx context.Context, dispatch verbDispatch, configArg, userArg, term string) ([]librarySearchHit, error) {
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" {
		return nil, fmt.Errorf("search: term required")
	}
	wsID, pjID := resolveSkillScopeBinding(ctx, configArg, userArg, "", "")

	type scopeKey struct{ scope, ws, pj string }
	scopes := []scopeKey{{scope: "library"}, {scope: "system"}}
	if wsID != "" {
		scopes = append(scopes, scopeKey{scope: "workspace", ws: wsID})
	}
	if pjID != "" && wsID != "" {
		scopes = append(scopes, scopeKey{scope: "project", ws: wsID, pj: pjID})
	}

	var hits []librarySearchHit
	for _, sk := range scopes {
		listReq, err := json.Marshal(docListRequest{Type: "skill", Scope: sk.scope, WorkspaceID: sk.ws, ProjectID: sk.pj, Limit: 200})
		if err != nil {
			return nil, err
		}
		raw, err := dispatch(ctx, "document_list", listReq)
		if err != nil {
			return nil, fmt.Errorf("search: list %s scope: %w", sk.scope, err)
		}
		var listed struct {
			Items []searchListItem `json:"items"`
		}
		if err := json.Unmarshal(raw, &listed); err != nil {
			return nil, fmt.Errorf("search: decode %s list: %w", sk.scope, err)
		}
		for _, it := range listed.Items {
			// Build the body-fetch key from the queried scope binding, not the
			// list item's own (possibly under-populated) workspace_id/project_id
			// — a project/workspace row whose item omits workspace_id would
			// otherwise fail document_get's scope-coherence check. Library is the
			// exception: it spans every publisher, so the publisher namespace
			// comes from the item's project_id.
			getWS, getPJ := sk.ws, sk.pj
			if sk.scope == "library" {
				getPJ = it.ProjectID
			}
			desc, err := skillDescription(ctx, dispatch, sk.scope, getWS, getPJ, it.Name)
			if err != nil {
				return nil, err
			}
			if !strings.Contains(strings.ToLower(it.Name), term) &&
				!strings.Contains(strings.ToLower(desc), term) {
				continue
			}
			publisher := "-"
			if it.Scope == "library" {
				publisher = it.ProjectID
			}
			hits = append(hits, librarySearchHit{
				Name:      it.Name,
				Publisher: publisher,
				Scope:     it.Scope,
				Version:   it.LatestVersion,
				Headline:  strings.TrimSpace(it.Headline),
			})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Name != hits[j].Name {
			return hits[i].Name < hits[j].Name
		}
		return hits[i].Publisher < hits[j].Publisher
	})
	return hits, nil
}

// skillDescription fetches one candidate's body and returns its frontmatter
// description (the search-matchable text the row itself does not carry). The
// caller passes a scope-coherent key (scope + the workspace_id/project_id that
// scope requires) so the document_get never trips the coherence check.
func skillDescription(ctx context.Context, dispatch verbDispatch, scope, wsID, pjID, name string) (string, error) {
	getReq, err := json.Marshal(struct {
		Name        string `json:"name"`
		Scope       string `json:"scope"`
		WorkspaceID string `json:"workspace_id,omitempty"`
		ProjectID   string `json:"project_id,omitempty"`
	}{Name: name, Scope: scope, WorkspaceID: wsID, ProjectID: pjID})
	if err != nil {
		return "", err
	}
	raw, err := dispatch(ctx, "document_get", getReq)
	if err != nil {
		return "", fmt.Errorf("search: get %s/%s: %w", scope, name, err)
	}
	var got struct {
		RawBody string `json:"raw_body"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return "", fmt.Errorf("search: decode get %s: %w", name, err)
	}
	fm, _, ferr := frontmatter.Parse([]byte(got.RawBody))
	if ferr != nil {
		return "", nil // unparseable frontmatter: still searchable by name
	}
	return fm.Description, nil
}

func renderSearchHits(out io.Writer, hits []librarySearchHit) {
	if len(hits) == 0 {
		fmt.Fprintln(out, "no skills matched")
		return
	}
	fmt.Fprintf(out, "%-32s %-14s %-10s %-4s %s\n", "NAME", "PUBLISHER", "SCOPE", "VER", "HEADLINE")
	for _, h := range hits {
		fmt.Fprintf(out, "%-32s %-14s %-10s v%-3d %s\n", h.Name, h.Publisher, h.Scope, h.Version, h.Headline)
	}
}

// newSkillAdoptCmd builds the `skill adopt <publisher>/<name>` command.
func newSkillAdoptCmd(configArg, userArg *string) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "adopt <publisher>/<name>",
		Short: "Fork a library skill into .satellites/skills/ as an ordinary project skill (forked-from stamped, no upstream link)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return adoptSkill(ctx, cmd.OutOrStdout(), args[0], *configArg, *userArg, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing .satellites/skills/<name>.md (the explicit confirmation; silent overwrite is refused)")
	return cmd
}

// adoptSkill fetches the library skill, strips its upstream link, stamps
// the fork provenance into the frontmatter, and writes the project copy.
func adoptSkill(ctx context.Context, out io.Writer, ref, configArg, userArg string, force bool) error {
	publisher, name, ok := strings.Cut(ref, "/")
	if !ok || strings.TrimSpace(publisher) == "" || strings.TrimSpace(name) == "" {
		return fmt.Errorf("adopt: identity must be <publisher>/<name>, got %q", ref)
	}
	getReq, err := json.Marshal(struct {
		Name      string `json:"name"`
		Scope     string `json:"scope"`
		ProjectID string `json:"project_id"`
	}{Name: name, Scope: "library", ProjectID: publisher})
	if err != nil {
		return err
	}
	raw, err := dispatchVerb(ctx, "document_get", getReq, configArg, userArg)
	if err != nil {
		return fmt.Errorf("adopt %s/%s: %w", publisher, name, err)
	}
	var got struct {
		RawBody  string `json:"raw_body"`
		Document struct {
			LatestVersion int `json:"latest_version"`
		} `json:"document"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return fmt.Errorf("adopt: decode get: %w", err)
	}

	dest := filepath.Join(substrateRoot, "skills", name+".md")
	if _, statErr := os.Stat(dest); statErr == nil && !force {
		return fmt.Errorf("adopt: %s already exists — silent overwrite refused; re-run with --force to confirm", dest)
	}

	// No upstream link remains: the publish stamp goes; the fork is owned
	// here from now on, with only the frontmatter provenance recording where
	// it came from.
	body := libraryStampLine.ReplaceAllString(got.RawBody, "")
	stamp := fmt.Sprintf("%s/%s@%d", publisher, name, got.Document.LatestVersion)
	body = injectForkedFrom(body, stamp)

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("adopt: %w", err)
	}
	if err := os.WriteFile(dest, []byte(body), 0o644); err != nil {
		return fmt.Errorf("adopt: %w", err)
	}
	fmt.Fprintf(out, "adopted %s → %s (forked-from: %s)\n", ref, dest, stamp)
	fmt.Fprintln(out, "now an ordinary project skill — iterate locally and `satellites skill upload` to own it")
	return nil
}

// forkedFromLine matches an existing forked-from frontmatter line so a
// --force re-adopt replaces rather than accumulates.
var forkedFromLine = regexp.MustCompile(`(?m)^forked-from:.*\n?`)

// injectForkedFrom returns body with exactly one
// `forked-from: <publisher>/<name>@<version>` frontmatter line, appended
// inside an existing frontmatter block (before its closing ---) or wrapped
// in a fresh block when the body carries none.
func injectForkedFrom(body, stamp string) string {
	body = forkedFromLine.ReplaceAllString(body, "")
	line := "forked-from: " + stamp + "\n"
	if strings.HasPrefix(body, "---\n") || strings.HasPrefix(body, "---\r\n") {
		rest := body[strings.IndexByte(body, '\n')+1:]
		if idx := strings.Index(rest, "\n---"); idx >= 0 {
			at := len(body) - len(rest) + idx + 1
			return body[:at] + line + body[at:]
		}
	}
	return "---\n" + line + "---\n\n" + body
}
