// `satellites context show <story>` — render the COMPLETE delivered context
// the substrate assembles for the agent, with each component labelled by
// source + size (bytes / ~tokens). The opacity fix (epic:qa-observability,
// story:sty_5893e03c): the agent runs on an assembled bundle that is today a
// black box to the operator; this makes it inspectable and doubles as the
// delivered-context baseline the budget ratchet (story:sty_e3e8f3ce) tracks.
//
// Grounded in what is ACTUALLY delivered, not a re-derivation (AC2): each
// component is read from the same source the substrate ships —
//   - load-context: the embedded markdown the MCP server serves verbatim
//   - skills index: the materialised .claude/skills/*/SKILL.md frontmatter
//   - principles ride-along: the curated `principles:* ∩ principles:always`
//     sidecar query (verb constants), via document_list/document_get
//   - story envelope + ## Workflow + recent ledger: the exact gate stdin
//     bundle (reviewGetStory + recentGateLedger, shared with the gate path)
//
// Operator-facing, out of band — nothing here is injected into the executor's
// turn (AC4). No new MCP verb (AC5): a client render over existing read verbs,
// the shared verb constants, and local artifacts.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/bobmcallan/satellites/config/documents"
	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// Partition labels group components by who holds the context and when. The
// grand total sums the delivered partitions (always-on + per-gate); a
// within-envelope row is shown for visibility but excluded from totals so
// the ## Workflow (a slice of the story body) is never double-counted.
const (
	partAlwaysOn       = "always-on"       // every session, unconditional (executor)
	partPerGate        = "per-gate"        // one isolated reviewer claude -p per transition
	partWithinEnvelope = "within-envelope" // a labelled slice of another component; not summed
)

// contextComponent is one measured piece of the delivered context.
type contextComponent struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	Partition string `json:"partition"`
	Bytes     int    `json:"bytes"`
	Tokens    int    `json:"approx_tokens"`
}

// deliveredContext is the assembled, sized bundle — the structure the
// --json form emits and the budget ratchet (order:10) consumes.
type deliveredContext struct {
	StoryID    string             `json:"story_id"`
	Components []contextComponent `json:"components"`
	// TotalsBytes is keyed by partition plus "grand" (always-on + per-gate).
	TotalsBytes map[string]int `json:"totals_bytes"`
}

// estTokens is the spike's documented heuristic (bytes ÷ 4). order:10 may
// refine it; kept here as the single token estimator for the view.
func estTokens(b int) int { return (b + 3) / 4 }

func newContextCmd(configArg, userArg *string) *cobra.Command {
	var asJSON bool
	show := &cobra.Command{
		Use:   "show <story-id>",
		Short: "Render the complete delivered context for a story, by source + size",
		Long: `show assembles the COMPLETE context the substrate delivers to the agent
for a story — the load-context instructions, the skills index, the principles
ride-along, the story envelope + its ## Workflow, and the recent ledger — and
prints each component with its source and size (bytes / ~tokens), grouped into
always-on (executor) vs per-gate (reviewer) context, with a grand total.

It reads each component from the same source the substrate ships, so the view is
the real bundle, not an approximation, and its totals are the delivered-context
baseline. --json emits the structured measure.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			dc, err := assembleDeliveredContext(ctx, strings.TrimSpace(args[0]), *configArg, *userArg)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(dc)
			}
			renderDeliveredContext(cmd.OutOrStdout(), dc)
			return nil
		},
	}
	show.Flags().BoolVar(&asJSON, "json", false, "Emit the structured delivered-context measure as JSON.")

	contextCmd := &cobra.Command{
		Use:   "context",
		Short: "Inspect the context the substrate delivers to the agent",
	}
	contextCmd.PersistentFlags().StringVar(configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	contextCmd.PersistentFlags().StringVar(userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID).")
	contextCmd.AddCommand(show)
	contextCmd.AddCommand(newContextReviewCmd(configArg, userArg))
	contextCmd.AddCommand(newContextCurateCmd(configArg, userArg))
	contextCmd.AddCommand(newContextBudgetCmd(configArg, userArg))
	contextCmd.AddCommand(newContextValidateCmd(configArg, userArg))
	return contextCmd
}

func init() {
	var (
		configArg string
		userArg   string
	)
	register(newContextCmd(&configArg, &userArg))
}

// assembleDeliveredContext builds the sized bundle by calling each
// component's owning source — never re-deriving content (AC2 + the data-
// ownership rule). Component failures are surfaced as a zero-size row with
// the error in the source, so the view never silently drops a component.
func assembleDeliveredContext(ctx context.Context, storyID, configPath, userArg string) (deliveredContext, error) {
	if storyID == "" {
		return deliveredContext{}, fmt.Errorf("story id required")
	}
	dc := deliveredContext{StoryID: storyID, TotalsBytes: map[string]int{}}
	add := func(name, source, partition string, n int) {
		dc.Components = append(dc.Components, contextComponent{
			Name: name, Source: source, Partition: partition, Bytes: n, Tokens: estTokens(n),
		})
	}

	// 1. Load-context (MCP instructions) — served verbatim from the embed.
	lc := documents.MCPLoadContextMarkdown()
	add("load-context (MCP instructions)",
		"config/documents/satellites_mcp_load_context.md (MCP initialize, served verbatim)",
		partAlwaysOn, len(lc))

	// 2. Skills index — the materialised .claude/skills frontmatter the agent
	// is shown (name + description; bodies excluded from the index).
	idxBytes, idxN, idxErr := skillsIndexSize()
	idxSrc := fmt.Sprintf(".claude/skills/*/SKILL.md frontmatter (%d skills, name+description)", idxN)
	if idxErr != nil {
		idxSrc = "skills index unavailable: " + idxErr.Error()
	}
	add("skills index", idxSrc, partAlwaysOn, idxBytes)

	// 3. Principles ride-along — the curated sidecar (scope tag ∩ always).
	story, body, err := reviewGetStory(ctx, reviewOpts{StoryID: storyID, ConfigPath: configPath, UserArg: userArg})
	if err != nil {
		return deliveredContext{}, err
	}
	prinBytes, prinN := principlesSidecarSize(ctx, story, configPath, userArg)
	add("principles ride-along (sidecar)",
		fmt.Sprintf("document_list principles:<scope> ∩ %s (%d curated)", verb.PrincipleTagAlways, prinN),
		partAlwaysOn, prinBytes)

	// 4. Story envelope (full body) — the gate stdin `story_body`.
	add("story envelope (body)",
		"document_get <story> RawBody (gate stdin story_body)",
		partPerGate, len(body))

	// 4b. The story's ## Workflow — a labelled slice of the envelope (shown,
	// not summed) so the operator sees the workflow the gates read.
	wf := extractSection(body, "## Workflow")
	add("story ## Workflow", "## Workflow block within the story body",
		partWithinEnvelope, len(wf))

	// 5. Recent ledger (≤5 rows) — the gate stdin `recent_ledger`, the exact
	// slice a gate run receives (shared assembly with the reviewer path).
	recentResp, recentSource := recentGateLedger(ctx, storyID, configPath, userArg)
	rl, _ := json.Marshal(recentResp.Entries)
	add(fmt.Sprintf("recent ledger (%d rows)", len(recentResp.Entries)),
		"gate stdin recent_ledger via "+recentSource, partPerGate, len(rl))

	// 6. Gate skill body(s) — the system prompt for each transition the
	// story's ## Workflow names, one per gate run.
	for _, skill := range reviewerSkillsFromWorkflow(wf) {
		n, serr := skillBodySize(skill)
		src := fmt.Sprintf(".claude/skills/%s/SKILL.md body (gate --append-system-prompt)", skill)
		if serr != nil {
			src = fmt.Sprintf("gate skill %s unavailable: %v", skill, serr)
		}
		add("gate skill body: "+skill, src, partPerGate, n)
	}

	dc.TotalsBytes = sumPartitionTotals(dc.Components)
	return dc, nil
}

// sumPartitionTotals tallies bytes per partition plus a "grand" total of the
// delivered partitions (always-on + per-gate). within-envelope rows are a
// labelled slice of another component and are excluded so nothing is
// double-counted. Pure over its input for direct testing.
func sumPartitionTotals(components []contextComponent) map[string]int {
	totals := map[string]int{}
	for _, c := range components {
		if c.Partition == partWithinEnvelope {
			continue
		}
		totals[c.Partition] += c.Bytes
	}
	totals["grand"] = totals[partAlwaysOn] + totals[partPerGate]
	return totals
}

// skillsIndexSize sums name+description across the materialised skills — the
// agent-facing index Claude Code surfaces. Each skill's contribution is
// `len(name)+len(description)+2` (the ": " + newline of an index line).
func skillsIndexSize() (bytes, count int, err error) {
	root := filepath.Join(".claude", "skills")
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(root, e.Name(), "SKILL.md"))
		if rerr != nil {
			continue
		}
		fm, _, perr := frontmatter.Parse(frontmatter.StripSyncStamp(raw))
		if perr != nil {
			continue
		}
		name := strings.TrimSpace(fm.Name)
		if name == "" {
			name = e.Name()
		}
		bytes += len(name) + len(strings.TrimSpace(fm.Description)) + 2
		count++
	}
	return bytes, count, nil
}

// ridealongPrincipleInfo is one principle in the curated ride-along sidecar —
// its identity, scope, and delivered body size.
type ridealongPrincipleInfo struct {
	Name  string `json:"name"`
	ID    string `json:"id"`
	Scope string `json:"scope"`
	Bytes int    `json:"bytes"`
}

// ridealongPrinciples enumerates the curated ride-along (principles tagged
// `principles:<scope>` ∩ `principles:always`) that apply to the story, with each
// principle's delivered body size — the exact sidecar membership the substrate
// would ship. Single owner: principlesSidecarSize sums it and `context curate`
// lists it. Best-effort per scope — a lookup failure on one scope contributes
// nothing rather than failing the caller.
func ridealongPrinciples(ctx context.Context, story reviewStory, configPath, userArg string) []ridealongPrincipleInfo {
	type scopeReq struct {
		scope       verb.PrincipleScope
		listScope   string
		workspaceID string
		projectID   string
	}
	reqs := []scopeReq{
		{scope: verb.PrincipleScopeGlobal, listScope: "system"},
		{scope: verb.PrincipleScopeWorkspace, listScope: "workspace", workspaceID: story.WorkspaceID},
		{scope: verb.PrincipleScopeProject, listScope: "project", workspaceID: story.WorkspaceID, projectID: story.ProjectID},
	}
	var out []ridealongPrincipleInfo
	for _, r := range reqs {
		if r.listScope == "workspace" && r.workspaceID == "" {
			continue
		}
		if r.listScope == "project" && (r.workspaceID == "" || r.projectID == "") {
			continue
		}
		listReq, err := json.Marshal(verb.DocumentListRequest{
			Type:        "document",
			Scope:       r.listScope,
			WorkspaceID: r.workspaceID,
			ProjectID:   r.projectID,
			Tags:        []string{verb.PrincipleTag(r.scope), verb.PrincipleTagAlways},
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
		var listResp verb.DocumentListResponse
		if err := json.Unmarshal(raw, &listResp); err != nil {
			continue
		}
		for _, item := range listResp.Items {
			getReq, err := json.Marshal(verb.DocumentGetRequest{ID: item.ID})
			if err != nil {
				continue
			}
			graw, err := dispatchVerb(ctx, "document_get", getReq, configPath, userArg)
			if err != nil {
				continue
			}
			var gresp verb.DocumentGetResponse
			if err := json.Unmarshal(graw, &gresp); err != nil {
				continue
			}
			pbody := gresp.RawBody
			if pbody == "" && len(gresp.Versions) > 0 {
				pbody = gresp.Versions[0].Body
			}
			out = append(out, ridealongPrincipleInfo{
				Name:  item.Name,
				ID:    item.ID,
				Scope: string(r.scope),
				Bytes: len(pbody),
			})
		}
	}
	return out
}

// principlesSidecarSize sums the curated ride-along — the always-on principle
// cost. Owner: ridealongPrinciples.
func principlesSidecarSize(ctx context.Context, story reviewStory, configPath, userArg string) (bytes, count int) {
	ps := ridealongPrinciples(ctx, story, configPath, userArg)
	for _, p := range ps {
		bytes += p.Bytes
	}
	return bytes, len(ps)
}

// skillBodySize reads a materialised gate skill's body (frontmatter +
// sync-stamp stripped) and returns its byte length — the system prompt a
// gate run receives for that skill.
func skillBodySize(skill string) (int, error) {
	raw, err := os.ReadFile(filepath.Join(".claude", "skills", skill, "SKILL.md"))
	if err != nil {
		return 0, err
	}
	_, body, err := frontmatter.Parse(frontmatter.StripSyncStamp(raw))
	if err != nil {
		return 0, err
	}
	return len(body), nil
}

// extractSection returns the markdown section beginning at the given `## `
// heading up to (but excluding) the next `## ` heading or end of body. Empty
// when the heading is absent.
func extractSection(body, heading string) string {
	lines := strings.Split(body, "\n")
	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == heading {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// reviewerSkillsFromWorkflow extracts the unique reviewer_skill names from a
// ## Workflow block by scanning for `reviewer_skill:` lines — a string scan,
// so the CLI never imports internal/workflow (layering guard). Duplicates
// dropped (a skill that gates more than one edge is one system-prompt body);
// sorted for stable output + tests.
func reviewerSkillsFromWorkflow(workflow string) []string {
	var out []string
	seen := map[string]bool{}
	for _, ln := range strings.Split(workflow, "\n") {
		idx := strings.Index(ln, "reviewer_skill:")
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(ln[idx+len("reviewer_skill:"):])
		rest = strings.Trim(rest, `"' }`)
		if rest != "" && !seen[rest] {
			seen[rest] = true
			out = append(out, rest)
		}
	}
	sort.Strings(out)
	return out
}

// renderDeliveredContext prints the grouped table with per-partition
// subtotals and a grand total.
func renderDeliveredContext(w io.Writer, dc deliveredContext) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "delivered context for %s\n\n", dc.StoryID)
	fmt.Fprintln(tw, "COMPONENT\tPARTITION\tBYTES\t~TOKENS\tSOURCE")
	order := []string{partAlwaysOn, partPerGate, partWithinEnvelope}
	for _, part := range order {
		for _, c := range dc.Components {
			if c.Partition != part {
				continue
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n", c.Name, c.Partition, c.Bytes, c.Tokens, c.Source)
		}
		if sub, ok := dc.TotalsBytes[part]; ok {
			fmt.Fprintf(tw, "  subtotal (%s)\t\t%d\t%d\t\n", part, sub, estTokens(sub))
		}
	}
	grand := dc.TotalsBytes["grand"]
	fmt.Fprintf(tw, "  GRAND TOTAL (always-on + per-gate)\t\t%d\t%d\t\n", grand, estTokens(grand))
	tw.Flush()
	fmt.Fprintf(w, "\n~tokens ≈ bytes/4 (heuristic). within-envelope rows are a slice of the story body and are excluded from totals.\n")
}
