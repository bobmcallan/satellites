// Top-menu Config page composite for story_644a2eb1. Surfaces the project
// Configuration documents (type=configuration) plus the resolved workflow,
// contracts, skills, and principles for the selected Configuration.
//
// Story_7b77ffb0 (portal UI for role-based execution) extends the
// composite with an `agents` section listing workspace + project agents
// alongside a per-phase allocation table sourced from the contract
// instances bound to a story whose Configuration matches the selected
// one.
package portal

import (
	"context"
	"sort"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/story"
)

const configListLimit = 200

// configCard is the dropdown view-model — id + name only.
type configCard struct {
	ID   string
	Name string
}

// phaseAllocationCard is one row in the per-phase allocation table:
// "preplan_agent ran preplan in story_xxx". The /config page renders
// these only when the selected Configuration has been claimed by at
// least one story (CIs whose contract_id matches one of the
// Configuration's ContractRefs).
type phaseAllocationCard struct {
	ContractName string
	ContractID   string
	AgentID      string
	AgentName    string
	AgentHref    string
	StoryID      string
	StoryHref    string
}

// configComposite is the view-model fed to configuration.html. When no
// Configuration documents exist the four resolved slices are empty and
// the template renders the empty state.
type configComposite struct {
	// Configurations lists every type=configuration document the caller
	// can see, sorted by Name. Empty when none exist.
	Configurations []configCard
	// SelectedID is the id of the active Configuration. Empty when no
	// Configuration is selected (either no Configurations exist or the
	// caller passed a non-existent id).
	SelectedID string
	// Selected is the resolved Configuration document.
	Selected document.Document
	// Workflow is the ordered list of contract documents pulled from
	// Selected's ContractRefs. Index = phase.
	Workflow []documentCard
	// Contracts mirrors Workflow but exposes the same docs as a flat
	// list so the template can render a "contracts" section that
	// matches v3's skills page structure.
	Contracts []documentCard
	// Skills resolves the SkillRefs of Selected. Empty when none.
	Skills []documentCard
	// Principles resolves the PrincipleRefs of Selected. Empty when none.
	Principles []documentCard
	// Agents lists workspace + project agents. Story_7b77ffb0.
	Agents []agentRow
	// PerPhaseAllocation lists, per workflow contract, the agent and
	// story that has used that contract under the selected
	// Configuration. Empty until at least one story claims the
	// Configuration's workflow. Story_7b77ffb0.
	PerPhaseAllocation []phaseAllocationCard
}

// buildConfigComposite assembles the composite for the /config page.
// `requestedID` is the `?id=` query param; when empty (or unresolved) the
// first Configuration in the sorted list is selected so a fresh visit
// always renders something.
func buildConfigComposite(ctx context.Context, docs document.Store, contracts contract.Store, stories story.Store, memberships []string, requestedID string) configComposite {
	out := configComposite{}
	if docs == nil {
		return out
	}

	rows, err := docs.List(ctx, document.ListOptions{Type: document.TypeConfiguration, Limit: configListLimit}, memberships)
	if err != nil || len(rows) == 0 {
		// Even when no Configuration exists, surface the agents section
		// so operators see the workspace's available agents.
		out.Agents = listAgentsForConfig(ctx, docs, memberships)
		return out
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })

	out.Configurations = make([]configCard, 0, len(rows))
	for _, r := range rows {
		out.Configurations = append(out.Configurations, configCard{ID: r.ID, Name: r.Name})
	}

	var selected document.Document
	for _, r := range rows {
		if r.ID == requestedID {
			selected = r
			break
		}
	}
	if selected.ID == "" {
		selected = rows[0]
	}
	out.Selected = selected
	out.SelectedID = selected.ID

	cfg, err := document.UnmarshalConfiguration(selected.Structured)
	if err != nil {
		out.Agents = listAgentsForConfig(ctx, docs, memberships)
		return out
	}

	out.Workflow = resolveCards(ctx, docs, memberships, cfg.ContractRefs)
	out.Contracts = out.Workflow
	out.Skills = resolveCards(ctx, docs, memberships, cfg.SkillRefs)
	out.Principles = resolveCards(ctx, docs, memberships, cfg.PrincipleRefs)
	out.Agents = listAgentsForConfig(ctx, docs, memberships)
	out.PerPhaseAllocation = perPhaseAllocationFor(ctx, docs, contracts, stories, selected.ID, out.Workflow, memberships)
	return out
}

// resolveCards looks up each id in the supplied slice and returns the
// cards in the same order. Ids that don't resolve are skipped silently
// — the substrate's validateConfigurationRefs enforces validity at
// write time, so a non-resolving id here means the doc was deleted out
// from under the Configuration after the fact.
func resolveCards(ctx context.Context, docs document.Store, memberships []string, ids []string) []documentCard {
	out := make([]documentCard, 0, len(ids))
	for _, id := range ids {
		d, err := docs.GetByID(ctx, id, memberships)
		if err != nil {
			continue
		}
		out = append(out, documentCardFor(d))
	}
	return out
}

// listAgentsForConfig returns the workspace + project agents the
// caller can see, projected into the agentRow shape. Mirrors
// buildAgentsComposite without the canonical-filter.
func listAgentsForConfig(ctx context.Context, docs document.Store, memberships []string) []agentRow {
	composite := buildAgentsComposite(ctx, docs, memberships, agentFilter{})
	return composite.Rows
}

// perPhaseAllocationFor returns the per-contract allocation rows for
// the workflow under the selected Configuration. For each contract in
// `workflow`, it finds the most-recent story whose ConfigurationID
// matches `configID`, picks the matching CI, and projects the agent +
// story link. Returns an empty slice when no story has claimed the
// Configuration yet.
func perPhaseAllocationFor(ctx context.Context, docs document.Store, contracts contract.Store, stories story.Store, configID string, workflow []documentCard, memberships []string) []phaseAllocationCard {
	if contracts == nil || stories == nil || configID == "" || len(workflow) == 0 {
		return []phaseAllocationCard{}
	}
	matching, err := stories.ListByConfigurationID(ctx, configID, memberships)
	if err != nil {
		return []phaseAllocationCard{}
	}
	if len(matching) == 0 {
		return []phaseAllocationCard{}
	}

	type ciRef struct {
		ci    contract.ContractInstance
		story story.Story
	}
	byContract := make(map[string]ciRef)
	for _, s := range matching {
		cis, err := contracts.List(ctx, s.ID, memberships)
		if err != nil {
			continue
		}
		for _, ci := range cis {
			ref, ok := byContract[ci.ContractID]
			if !ok || ci.UpdatedAt.After(ref.ci.UpdatedAt) {
				byContract[ci.ContractID] = ciRef{ci: ci, story: s}
			}
		}
	}

	agentNames := make(map[string]string)
	resolveAgent := func(id string) string {
		if id == "" || docs == nil {
			return ""
		}
		if v, ok := agentNames[id]; ok {
			return v
		}
		d, err := docs.GetByID(ctx, id, memberships)
		if err != nil {
			agentNames[id] = ""
			return ""
		}
		agentNames[id] = d.Name
		return d.Name
	}

	out := make([]phaseAllocationCard, 0, len(workflow))
	for _, c := range workflow {
		ref, ok := byContract[c.ID]
		if !ok {
			out = append(out, phaseAllocationCard{
				ContractName: c.Name,
				ContractID:   c.ID,
			})
			continue
		}
		card := phaseAllocationCard{
			ContractName: c.Name,
			ContractID:   c.ID,
			AgentID:      ref.ci.AgentID,
			StoryID:      ref.story.ID,
		}
		if card.AgentID != "" {
			card.AgentName = resolveAgent(card.AgentID)
			card.AgentHref = "/documents/" + card.AgentID
		}
		card.StoryHref = "/projects/" + ref.story.ProjectID + "/stories/" + ref.story.ID
		out = append(out, card)
	}
	return out
}
