// Top-menu Config page composite for story_644a2eb1. Surfaces the project
// Configuration documents (type=configuration) plus the resolved workflow,
// contracts, skills, and principles for the selected Configuration.
package portal

import (
	"context"
	"sort"

	"github.com/bobmcallan/satellites/internal/document"
)

const configListLimit = 200

// configCard is the dropdown view-model — id + name only.
type configCard struct {
	ID   string
	Name string
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
}

// buildConfigComposite assembles the composite for the /config page.
// `requestedID` is the `?id=` query param; when empty (or unresolved) the
// first Configuration in the sorted list is selected so a fresh visit
// always renders something.
func buildConfigComposite(ctx context.Context, docs document.Store, memberships []string, requestedID string) configComposite {
	out := configComposite{}
	if docs == nil {
		return out
	}

	rows, err := docs.List(ctx, document.ListOptions{Type: document.TypeConfiguration, Limit: configListLimit}, memberships)
	if err != nil || len(rows) == 0 {
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
		return out
	}

	out.Workflow = resolveCards(ctx, docs, memberships, cfg.ContractRefs)
	out.Contracts = out.Workflow
	out.Skills = resolveCards(ctx, docs, memberships, cfg.SkillRefs)
	out.Principles = resolveCards(ctx, docs, memberships, cfg.PrincipleRefs)
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
