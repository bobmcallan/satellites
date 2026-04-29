// Top-menu Config page composite. After story_09c4086c (S4) deleted
// type=configuration, the page is a catalog view of system + workspace
// agents/contracts/workflows/principles plus the workspace's available
// agents. The orchestrator-emergent plan model (design ldg_81b5b9da)
// replaces the previous stored-Configuration binding.
package portal

import (
	"context"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
)

const configListLimit = 200

// configComposite is the view-model fed to configuration.html. The
// System* slices carry the configseed-loaded scope=system docs; Agents
// carries workspace + project agents available to the caller.
type configComposite struct {
	// Agents lists workspace + project agents.
	Agents []agentRow
	// SystemContracts lists every scope=system, type=contract document
	// the configseed loader writes.
	SystemContracts []documentCard
	// SystemAgents lists every scope=system, type=agent document.
	SystemAgents []agentRow
	// SystemWorkflows lists every scope=system, type=workflow document.
	SystemWorkflows []documentCard
	// SystemPrinciples lists every scope=system, type=principle document.
	// story_09c4086c (S4) added this to surface the principle catalog
	// alongside contracts/workflows/agents now that the type=configuration
	// panel is gone.
	SystemPrinciples []documentCard
	// MandateStack is the active workflow scope-mandate view introduced
	// by story_f0a78759 (S5): system → workspace → project → user
	// workflow markdowns plus the merged effective list.
	MandateStack mandateStack
}

// mandateStack is the per-tier view of type=workflow documents that
// drive the additive scope-mandate chain plus the merged effective
// required_slots list returned by contract.MergeSlots.
type mandateStack struct {
	System    []mandateLayer
	Workspace []mandateLayer
	Project   []mandateLayer
	User      []mandateLayer
	Effective []mandateSlotRow
}

// mandateLayer is one workflow document at a given scope, surfaced to
// the portal so the caller can see which markdowns contributed slots.
type mandateLayer struct {
	DocumentID string
	Name       string
	Scope      string
	Slots      []mandateSlotRow
}

// mandateSlotRow is the per-slot row rendered under each layer plus the
// merged effective list.
type mandateSlotRow struct {
	ContractName string
	Required     bool
	MinCount     int
	MaxCount     int
	Source       string
}

// buildConfigComposite assembles the composite for the /config page.
// workspaceID/projectID/userID drive the active mandate stack panel
// (story_f0a78759). projectID and userID may be empty when the caller
// has no project context bound on the active workspace.
func buildConfigComposite(ctx context.Context, docs document.Store, memberships []string, workspaceID, projectID, userID string) configComposite {
	out := configComposite{}
	if docs == nil {
		return out
	}

	out.SystemContracts = listSystemDocuments(ctx, docs, document.TypeContract)
	out.SystemWorkflows = listSystemDocuments(ctx, docs, document.TypeWorkflow)
	out.SystemAgents = listSystemAgents(ctx, docs)
	out.SystemPrinciples = listSystemDocuments(ctx, docs, document.TypePrinciple)
	out.Agents = listAgentsForConfig(ctx, docs, memberships)
	out.MandateStack = buildMandateStack(ctx, docs, memberships, workspaceID, projectID, userID)
	return out
}

// buildMandateStack loads workflow documents at each scope tier and
// composes the merged effective slot list via contract.MergeSlots.
// Callers pass workspaceID/projectID/userID to scope the lower tiers;
// any of them may be empty.
func buildMandateStack(ctx context.Context, docs document.Store, memberships []string, workspaceID, projectID, userID string) mandateStack {
	system := listWorkflowLayer(ctx, docs, document.ScopeSystem, "", "", memberships)
	workspace := listWorkflowLayer(ctx, docs, document.ScopeWorkspace, "", "", memberships)
	project := listWorkflowLayer(ctx, docs, document.ScopeProject, projectID, "", memberships)
	user := listWorkflowLayer(ctx, docs, document.ScopeUser, "", userID, memberships)

	merged := contract.MergeSlots(
		contract.LayerSlots{Source: contract.SourceSystem, Slots: collectSlots(system)},
		contract.LayerSlots{Source: contract.SourceWorkspace, Slots: collectSlots(workspace)},
		contract.LayerSlots{Source: contract.SourceProject, Slots: collectSlots(project)},
		contract.LayerSlots{Source: contract.SourceUser, Slots: collectSlots(user)},
	)
	effective := make([]mandateSlotRow, 0, len(merged.Slots))
	for _, slot := range merged.Slots {
		effective = append(effective, mandateSlotRow{
			ContractName: slot.ContractName,
			Required:     slot.Required,
			MinCount:     slot.MinCount,
			MaxCount:     slot.MaxCount,
			Source:       slot.Source,
		})
	}
	_ = workspaceID
	return mandateStack{
		System:    system,
		Workspace: workspace,
		Project:   project,
		User:      user,
		Effective: effective,
	}
}

// listWorkflowLayer reads workflow documents at a given scope tier and
// projects them into mandateLayer rows for the portal. scope=system
// reads with nil memberships (globally readable per pr_0779e5af);
// other tiers are workspace-scoped via the caller's memberships.
func listWorkflowLayer(ctx context.Context, docs document.Store, scope, projectID, userID string, memberships []string) []mandateLayer {
	if docs == nil {
		return nil
	}
	opts := document.ListOptions{Type: document.TypeWorkflow, Scope: scope, Limit: configListLimit}
	if scope == document.ScopeProject {
		opts.ProjectID = projectID
	}
	listMemberships := memberships
	if scope == document.ScopeSystem {
		listMemberships = nil
	}
	rows, err := docs.List(ctx, opts, listMemberships)
	if err != nil {
		return nil
	}
	out := make([]mandateLayer, 0, len(rows))
	for _, d := range rows {
		if d.Status != document.StatusActive {
			continue
		}
		if scope == document.ScopeUser && (userID == "" || d.CreatedBy != userID) {
			continue
		}
		slots := contract.SlotsFromWorkflowDocStructured(d.Structured)
		layerRows := make([]mandateSlotRow, 0, len(slots))
		for _, s := range slots {
			layerRows = append(layerRows, mandateSlotRow{
				ContractName: s.ContractName,
				Required:     s.Required,
				MinCount:     s.MinCount,
				MaxCount:     s.MaxCount,
				Source:       scope,
			})
		}
		out = append(out, mandateLayer{
			DocumentID: d.ID,
			Name:       d.Name,
			Scope:      scope,
			Slots:      layerRows,
		})
	}
	return out
}

func collectSlots(layers []mandateLayer) []contract.Slot {
	out := make([]contract.Slot, 0)
	for _, layer := range layers {
		for _, s := range layer.Slots {
			out = append(out, contract.Slot{
				ContractName: s.ContractName,
				Required:     s.Required,
				MinCount:     s.MinCount,
				MaxCount:     s.MaxCount,
			})
		}
	}
	return out
}

// listAgentsForConfig returns the workspace + project agents the
// caller can see, projected into the agentRow shape.
func listAgentsForConfig(ctx context.Context, docs document.Store, memberships []string) []agentRow {
	composite := buildAgentsComposite(ctx, docs, memberships, agentFilter{})
	return composite.Rows
}

// listSystemDocuments returns every active scope=system document of the
// given type. Read with nil memberships because system-scope content is
// globally readable inside the workspace per pr_0779e5af.
func listSystemDocuments(ctx context.Context, docs document.Store, docType string) []documentCard {
	if docs == nil {
		return nil
	}
	rows, err := docs.List(ctx, document.ListOptions{
		Type:  docType,
		Scope: document.ScopeSystem,
		Limit: configListLimit,
	}, nil)
	if err != nil {
		return nil
	}
	out := make([]documentCard, 0, len(rows))
	for _, r := range rows {
		if r.Status != document.StatusActive {
			continue
		}
		out = append(out, documentCardFor(r))
	}
	return out
}

// listSystemAgents reuses buildAgentsComposite with nil memberships
// (bypasses the workspace filter) and keeps only the scope=system rows.
func listSystemAgents(ctx context.Context, docs document.Store) []agentRow {
	if docs == nil {
		return nil
	}
	all := buildAgentsComposite(ctx, docs, nil, agentFilter{}).Rows
	out := make([]agentRow, 0, len(all))
	for _, row := range all {
		if row.Scope != document.ScopeSystem {
			continue
		}
		out = append(out, row)
	}
	return out
}
