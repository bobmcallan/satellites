// Project configuration composite for story_433d0661. Surfaces contracts
// and skills (lifecycle-config document types) on a project-scoped page,
// peer to the workspace search delivered in story_6a0aca5a. Read-only.
package portal

import (
	"context"
	"sort"

	"github.com/bobmcallan/satellites/internal/agentprocess"
	"github.com/bobmcallan/satellites/internal/document"
)

// projectConfigurationComposite is the view-model for the Configuration
// page. Sections: contracts, skills, and (sty_e1ab884d) the resolved
// agent-process markdown.
type projectConfigurationComposite struct {
	Contracts      []documentCard
	Skills         []documentCard
	ContractsTotal int
	SkillsTotal    int

	// AgentProcessBody is the resolved markdown the MCP handshake
	// surfaces to agents. Sourced from the project-scope override
	// when present, falling back to the system default. Empty when
	// neither is seeded (panel renders the not-configured empty state).
	AgentProcessBody string
	// AgentProcessIsOverride is true when the project carries its own
	// agent_process artifact. False means the panel is rendering the
	// system-scope default body.
	AgentProcessIsOverride bool
	// AgentProcessOverrideDocID identifies the project's per-project
	// override doc when AgentProcessIsOverride is true. Empty otherwise.
	// Lets the panel link to /documents/<id> for editing.
	AgentProcessOverrideDocID string
}

// buildProjectConfigurationComposite assembles the composite from the
// document store. Contracts and skills are loaded across project + system
// scopes (the same merge pattern as the workspace search) so global
// lifecycle config shows alongside the project's own. Read-only — no
// edit surface from this page.
func buildProjectConfigurationComposite(ctx context.Context, docs document.Store, projectID string, memberships []string) projectConfigurationComposite {
	out := projectConfigurationComposite{}
	if docs == nil {
		return out
	}

	out.Contracts = collectConfigCards(ctx, docs, projectID, document.TypeContract, memberships)
	out.ContractsTotal = len(out.Contracts)

	out.Skills = collectConfigCards(ctx, docs, projectID, document.TypeSkill, memberships)
	out.SkillsTotal = len(out.Skills)

	// sty_e1ab884d: resolve the agent-process markdown shown in the
	// "agent process" panel. Project-scope override wins; otherwise the
	// system-default body is rendered inline so the panel always shows
	// the agent the actual handshake content.
	if projectID != "" {
		if override, err := docs.GetByName(ctx, projectID, agentprocess.ProjectOverrideName, memberships); err == nil && override.Type == document.TypeArtifact && override.Status == document.StatusActive {
			for _, t := range override.Tags {
				if t == agentprocess.KindTag {
					out.AgentProcessBody = override.Body
					out.AgentProcessIsOverride = true
					out.AgentProcessOverrideDocID = override.ID
					break
				}
			}
		}
	}
	if out.AgentProcessBody == "" {
		out.AgentProcessBody = agentprocess.Resolve(ctx, docs, "", memberships)
	}

	return out
}

// collectConfigCards loads a single document type across project + system
// scopes, dedupes by id, sorts by UpdatedAt desc. Returns an empty slice
// on any List error so the page still renders.
func collectConfigCards(ctx context.Context, docs document.Store, projectID, docType string, memberships []string) []documentCard {
	rows := loadConfigDocs(ctx, docs, projectID, docType, memberships)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].UpdatedAt.After(rows[j].UpdatedAt) })
	out := make([]documentCard, 0, len(rows))
	for _, d := range rows {
		out = append(out, documentCardFor(d))
	}
	return out
}

// loadConfigDocs runs two List calls (project + system scope) for the
// supplied type and merges the results, deduping by id. A nil projectID
// skips the project scope.
func loadConfigDocs(ctx context.Context, docs document.Store, projectID, docType string, memberships []string) []document.Document {
	seen := make(map[string]struct{})
	out := make([]document.Document, 0)
	if projectID != "" {
		projectRows, err := docs.List(ctx, document.ListOptions{Type: docType, ProjectID: projectID, Limit: projectWorkspaceListLimit}, memberships)
		if err == nil {
			for _, d := range projectRows {
				if _, dup := seen[d.ID]; dup {
					continue
				}
				seen[d.ID] = struct{}{}
				out = append(out, d)
			}
		}
	}
	systemRows, err := docs.List(ctx, document.ListOptions{Type: docType, Scope: document.ScopeSystem, Limit: projectWorkspaceListLimit}, memberships)
	if err == nil {
		for _, d := range systemRows {
			if _, dup := seen[d.ID]; dup {
				continue
			}
			seen[d.ID] = struct{}{}
			out = append(out, d)
		}
	}
	return out
}
