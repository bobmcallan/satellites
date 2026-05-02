package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// handleStoryExportWalk implements story_export_walk: render a story's
// contract walk as paste-ready markdown for PR descriptions, delivery
// reports, and stakeholder hand-offs. Reuses buildTaskWalk so the
// markdown matches the JSON walk verbatim. sty_a248f4df.
func (s *Server) handleStoryExportWalk(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if s.stories == nil || s.contracts == nil {
		return mcpgo.NewToolResultError("story_export_walk unavailable: story or contract store missing"), nil
	}
	storyID, err := req.RequireString("story_id")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	format := req.GetString("format", "markdown")
	if format != "markdown" {
		return mcpgo.NewToolResultError(fmt.Sprintf("story_export_walk: unsupported format %q", format)), nil
	}
	caller, _ := UserFrom(ctx)
	memberships := s.resolveCallerMemberships(ctx, caller)

	st, err := s.stories.GetByID(ctx, storyID, memberships)
	if err != nil {
		body, _ := json.Marshal(map[string]any{"error": "story_not_found"})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	walk, err := s.buildTaskWalk(ctx, storyID, memberships)
	if err != nil {
		body, _ := json.Marshal(map[string]any{"error": err.Error()})
		return mcpgo.NewToolResultError(string(body)), nil
	}
	configurationName := walk.ConfigurationName
	if configurationName == "" {
		configurationName = "project default"
	}
	content := renderWalkMarkdown(walk, st.Description, st.AcceptanceCriteria, st.Priority, st.Tags, configurationName)
	body, _ := json.Marshal(map[string]any{
		"filename": slugifyStoryFilename(st.ID, st.Title) + "-walk.md",
		"content":  content,
		"format":   format,
	})
	return mcpgo.NewToolResultText(string(body)), nil
}

// renderWalkMarkdown formats walk + story metadata into the
// "process followed" markdown shape from the AC. Iteration loops
// collapse under one H2 header carrying the ×N suffix; per-CI rows
// become H3 sections within the group. Deterministic for snapshot
// testing — the source list is already in workflow order from
// buildTaskWalk.
func renderWalkMarkdown(walk taskWalkResponse, description, ac, priority string, tags []string, configurationName string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s — %s\n", walk.Story.ID, walk.Story.Title)
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "**Status:** %s", emptyDash(walk.Story.Status))
	if priority != "" {
		fmt.Fprintf(&sb, "   **Priority:** %s", priority)
	}
	if len(tags) > 0 {
		fmt.Fprintf(&sb, "   **Tags:** %s", strings.Join(tags, ", "))
	}
	sb.WriteString("\n\n")
	if strings.TrimSpace(ac) != "" {
		sb.WriteString("## Acceptance criteria\n\n")
		sb.WriteString(strings.TrimRight(ac, "\n"))
		sb.WriteString("\n\n")
	}
	if strings.TrimSpace(description) != "" {
		sb.WriteString("## Description\n\n")
		sb.WriteString(strings.TrimRight(description, "\n"))
		sb.WriteString("\n\n")
	}
	sb.WriteString("---\n\n")

	// Group iteration loops under a single H2 header. The group order
	// follows first appearance in the workflow; cards within group
	// follow CreatedAt order (already sorted by buildTaskWalk).
	type group struct {
		name    string
		role    string
		entries []taskWalkCI
	}
	groups := []group{}
	groupIndex := map[string]int{}
	for _, ci := range walk.ContractInstances {
		idx, ok := groupIndex[ci.ContractName]
		if !ok {
			groupIndex[ci.ContractName] = len(groups)
			groups = append(groups, group{name: ci.ContractName, role: ci.RequiredRole, entries: []taskWalkCI{ci}})
			continue
		}
		groups[idx].entries = append(groups[idx].entries, ci)
	}
	for _, g := range groups {
		header := fmt.Sprintf("## %s", g.name)
		if len(g.entries) > 1 {
			header += fmt.Sprintf(" ×%d (loop)", len(g.entries))
		}
		sb.WriteString(header)
		sb.WriteString("\n\n")
		for _, ci := range g.entries {
			role := ci.RequiredRole
			if role == "" {
				role = "any"
			}
			outcome := ci.Outcome
			if outcome == "" {
				outcome = ci.Status
			}
			fmt.Fprintf(&sb, "### %s #%d   role=%s   %s", g.name, ci.Iteration, role, outcome)
			if ci.ClaimedAt != nil {
				fmt.Fprintf(&sb, "   %s", formatTimestamp(*ci.ClaimedAt))
				if ci.ClosedAt != nil {
					fmt.Fprintf(&sb, " → %s", formatTimestamp(*ci.ClosedAt))
				}
			} else if ci.ClosedAt != nil {
				fmt.Fprintf(&sb, "   closed %s", formatTimestamp(*ci.ClosedAt))
			}
			if ci.ClaimedByUser != "" {
				fmt.Fprintf(&sb, "   by %s", ci.ClaimedByUser)
			} else if ci.ClaimedBySession != "" {
				fmt.Fprintf(&sb, "   session %s", ci.ClaimedBySession)
			}
			sb.WriteString("\n")
			fmt.Fprintf(&sb, "- ledger rows: %d\n", ci.LedgerRowCount)
			if ci.PlanLedgerID != "" {
				fmt.Fprintf(&sb, "- plan: `%s`\n", ci.PlanLedgerID)
			}
			if ci.CloseLedgerID != "" {
				fmt.Fprintf(&sb, "- close: `%s`\n", ci.CloseLedgerID)
			}
			summary := ci.TaskSummary
			if summary.Enqueued+summary.InFlight+summary.ClosedSuccess+summary.ClosedFailure+summary.ClosedTimeout > 0 {
				fmt.Fprintf(&sb, "- tasks: enqueued=%d in_flight=%d closed_success=%d closed_failure=%d closed_timeout=%d\n",
					summary.Enqueued, summary.InFlight, summary.ClosedSuccess, summary.ClosedFailure, summary.ClosedTimeout)
			}
			sb.WriteString("\n")
		}
	}
	sb.WriteString("---\n\n")
	fmt.Fprintf(&sb, "Process defined by: %s\n", configurationName)
	return sb.String()
}

func formatTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04")
}

func emptyDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// slugifyStoryFilename converts a story title into a kebab-case slug
// suitable for a filename. Falls back to the story id when the title
// reduces to an empty string.
func slugifyStoryFilename(id, title string) string {
	var sb strings.Builder
	prevDash := true
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				sb.WriteRune('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(sb.String(), "-")
	if slug == "" {
		return id
	}
	if id != "" {
		return id + "-" + slug
	}
	return slug
}
