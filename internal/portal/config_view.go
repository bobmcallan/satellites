// Top-menu Config page composite. After story_09c4086c (S4) deleted
// type=configuration, the page is a catalog view of system + workspace
// agents/contracts/workflows/principles plus the workspace's available
// agents. The orchestrator-emergent plan model (design ldg_81b5b9da)
// replaces the previous stored-Configuration binding.
package portal

import (
	"context"

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
}

// buildConfigComposite assembles the composite for the /config page.
func buildConfigComposite(ctx context.Context, docs document.Store, memberships []string) configComposite {
	out := configComposite{}
	if docs == nil {
		return out
	}

	out.SystemContracts = listSystemDocuments(ctx, docs, document.TypeContract)
	out.SystemWorkflows = listSystemDocuments(ctx, docs, document.TypeWorkflow)
	out.SystemAgents = listSystemAgents(ctx, docs)
	out.SystemPrinciples = listSystemDocuments(ctx, docs, document.TypePrinciple)
	out.Agents = listAgentsForConfig(ctx, docs, memberships)
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
