package agentdispatch

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/agentprocess"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/task"
)

// Prompt section anchors. Tests assert each anchor is present in the
// assembled prompt — proves all six sources from the AC made it in.
const (
	AnchorAgentProcess = "## Agent Process"
	AnchorAgentProfile = "## Your Agent Profile"
	AnchorPrinciples   = "## Active Principles"
	AnchorStoryContext = "## Story Context"
	AnchorContract     = "## Contract"
	AnchorTaskChain    = "## Task Chain"
)

// composePrompt assembles the dispatched agent's full system prompt
// from the six load-bearing sources named in pr_substrate_provides_context.
// Missing sources degrade silently to an empty section under the
// section's anchor — the agent always sees the same outline shape so
// it can route on the headings.
//
// Header order:
//
//	## Agent Process       (artifact body — fundamentals + dispatch loop)
//	## Your Agent Profile  (agent doc body — role, voice, capability)
//	## Active Principles   (system + project principles, alphabetical)
//	## Story Context       (story header + AC + recent ledger evidence)
//	## Contract            (contract body for the task's action)
//	## Task Chain          (ordered task_walk excerpt with prior_task_id)
func composePrompt(ctx context.Context, deps Deps, t task.Task, agentDoc document.Document) string {
	var b strings.Builder

	b.WriteString(AnchorAgentProcess)
	b.WriteString("\n\n")
	b.WriteString(resolveAgentProcess(ctx, deps, t.ProjectID))
	b.WriteString("\n\n")

	b.WriteString(AnchorAgentProfile)
	b.WriteString("\n\n")
	b.WriteString(agentDoc.Body)
	b.WriteString("\n\n")

	b.WriteString(AnchorPrinciples)
	b.WriteString("\n\n")
	b.WriteString(formatPrinciples(ctx, deps, t.ProjectID))
	b.WriteString("\n\n")

	b.WriteString(AnchorStoryContext)
	b.WriteString("\n\n")
	b.WriteString(formatStoryContext(ctx, deps, t))
	b.WriteString("\n\n")

	b.WriteString(AnchorContract)
	b.WriteString("\n\n")
	b.WriteString(resolveContract(ctx, deps, t.Action))
	b.WriteString("\n\n")

	b.WriteString(AnchorTaskChain)
	b.WriteString("\n\n")
	b.WriteString(formatTaskChain(ctx, deps, t))
	b.WriteString("\n")

	return b.String()
}

func resolveAgentProcess(ctx context.Context, deps Deps, projectID string) string {
	if deps.Docs == nil {
		return "_(agent_process unavailable: doc store not wired)_"
	}
	body := agentprocess.Resolve(ctx, deps.Docs, projectID, nil)
	if body == "" {
		return "_(agent_process artifact not seeded)_"
	}
	return body
}

func formatPrinciples(ctx context.Context, deps Deps, projectID string) string {
	if deps.Docs == nil {
		return "_(principles unavailable: doc store not wired)_"
	}
	rows, err := deps.Docs.List(ctx, document.ListOptions{
		Type: document.TypePrinciple,
	}, nil)
	if err != nil || len(rows) == 0 {
		return "_(no active principles found)_"
	}
	// Filter to active rows in (system-scope or project-scope when
	// projectID matches).
	picked := make([]document.Document, 0, len(rows))
	for _, r := range rows {
		if r.Status != document.StatusActive {
			continue
		}
		if r.Scope == document.ScopeSystem {
			picked = append(picked, r)
			continue
		}
		if r.Scope == document.ScopeProject && projectID != "" && r.ProjectID != nil && *r.ProjectID == projectID {
			picked = append(picked, r)
		}
	}
	if len(picked) == 0 {
		return "_(no principles in scope)_"
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].Name < picked[j].Name })
	var b strings.Builder
	for _, p := range picked {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", p.Name, strings.TrimSpace(p.Body))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatStoryContext(ctx context.Context, deps Deps, t task.Task) string {
	if t.StoryID == "" {
		return "_(task is not bound to a story)_"
	}
	if deps.Stories == nil {
		return "_(story store unavailable)_"
	}
	st, err := deps.Stories.GetByID(ctx, t.StoryID, nil)
	if err != nil {
		return fmt.Sprintf("_(story %s not found: %v)_", t.StoryID, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**Story:** %s — `%s`\n", st.Title, st.ID)
	fmt.Fprintf(&b, "**Status:** %s\n", st.Status)
	if st.Priority != "" {
		fmt.Fprintf(&b, "**Priority:** %s\n", st.Priority)
	}
	if len(st.Tags) > 0 {
		fmt.Fprintf(&b, "**Tags:** %s\n", strings.Join(st.Tags, ", "))
	}
	b.WriteString("\n")
	if st.Description != "" {
		b.WriteString("### Why\n\n")
		b.WriteString(strings.TrimSpace(st.Description))
		b.WriteString("\n\n")
	}
	if st.AcceptanceCriteria != "" {
		b.WriteString("### Acceptance Criteria\n\n")
		b.WriteString(strings.TrimSpace(st.AcceptanceCriteria))
		b.WriteString("\n\n")
	}
	if deps.Projects != nil {
		if p, perr := deps.Projects.GetByID(ctx, st.ProjectID, nil); perr == nil {
			fmt.Fprintf(&b, "**Project:** %s (`%s`)\n\n", p.Name, p.ID)
		}
	}
	if deps.Ledger != nil {
		entries, lerr := deps.Ledger.List(ctx, st.ProjectID, ledger.ListOptions{
			StoryID: st.ID,
			Limit:   recentEvidenceLimit,
		}, nil)
		if lerr == nil && len(entries) > 0 {
			b.WriteString("### Recent Ledger Evidence\n\n")
			for _, e := range entries {
				tags := strings.Join(e.Tags, " ")
				excerpt := strings.TrimSpace(e.Content)
				if len(excerpt) > 240 {
					excerpt = excerpt[:240] + "…"
				}
				fmt.Fprintf(&b, "- `%s` [%s] %s — %s\n", e.ID, e.Type, tags, excerpt)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func resolveContract(ctx context.Context, deps Deps, action string) string {
	name := contractNameFromAction(action)
	if name == "" {
		return fmt.Sprintf("_(no contract resolves from action %q)_", action)
	}
	if deps.Docs == nil {
		return "_(contract unavailable: doc store not wired)_"
	}
	rows, err := deps.Docs.List(ctx, document.ListOptions{
		Type:  document.TypeContract,
		Scope: document.ScopeSystem,
	}, nil)
	if err != nil {
		return fmt.Sprintf("_(contract list failed: %v)_", err)
	}
	for _, r := range rows {
		if r.Status == document.StatusActive && r.Name == name {
			return fmt.Sprintf("**Contract:** %s\n\n%s", r.Name, strings.TrimSpace(r.Body))
		}
	}
	return fmt.Sprintf("_(contract %q not found in system scope)_", name)
}

func formatTaskChain(ctx context.Context, deps Deps, t task.Task) string {
	if t.StoryID == "" || deps.Tasks == nil {
		return "_(no task chain — task lacks story binding or store unwired)_"
	}
	rows, err := deps.Tasks.List(ctx, task.ListOptions{
		StoryID: t.StoryID,
		Limit:   200,
	}, nil)
	if err != nil {
		return fmt.Sprintf("_(task chain list failed: %v)_", err)
	}
	if len(rows) == 0 {
		return "_(task chain empty)_"
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.Before(rows[j].CreatedAt) })
	var b strings.Builder
	b.WriteString("| # | id | kind | action | status | prior_task_id |\n")
	b.WriteString("|---|----|------|--------|--------|---------------|\n")
	for i, r := range rows {
		marker := ""
		if r.ID == t.ID {
			marker = " ← THIS TASK"
		}
		fmt.Fprintf(&b, "| %d | `%s` | %s | %s | %s | `%s` |%s\n",
			i+1, r.ID, kindOrDash(r.Kind), r.Action, r.Status, r.PriorTaskID, marker)
	}
	return strings.TrimRight(b.String(), "\n")
}

func contractNameFromAction(action string) string {
	const prefix = "contract:"
	if len(action) <= len(prefix) || !strings.HasPrefix(action, prefix) {
		return ""
	}
	return action[len(prefix):]
}

func kindOrDash(k string) string {
	if k == "" {
		return "-"
	}
	return k
}

// roleFromAgentDoc extracts the agent's role name (used for audit
// header attribution `X-Satellites-Agent: <role>:<task_id>`). The
// agent doc's Name is the canonical role string.
func roleFromAgentDoc(d document.Document) string {
	if d.Name == "" {
		return "unknown"
	}
	return d.Name
}

// recentEvidenceLimit caps the recent_evidence section. Mirrors
// internal/mcpserver/story_context.go's bound so dispatched agents see
// the same window the orchestrator does.
const recentEvidenceLimit = 10

// projectStoreType + storyStoreType are exported solely so the Deps
// struct's interface fields stay tied to the canonical store packages
// without forcing callers to import this file.
type (
	projectStoreType = project.Store
	storyStoreType   = story.Store
)
