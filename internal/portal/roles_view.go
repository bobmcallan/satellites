// Roles + agents + grants browser composites for slice 6.7
// (story_5cc349a9). Three pages share this file: roles index, agents
// index, live grants panel. Per docs/ui-design.md#roles.
//
// Story_7b77ffb0 (portal UI for role-based execution) extends agentRow
// with permission_patterns, skill_refs, ephemeral, owning_story_id, and
// surfaces a promote-to-canonical hint computed from the same shape
// agent_ephemeral_summary returns.
package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/rolegrant"
)

// promoteCanonicalThreshold is the count of ephemeral agents sharing a
// sorted skill_refs set above which the /agents page renders a
// promote-to-canonical CTA. Matches the substrate's
// agent_ephemeral_summary handler so portal + MCP surfaces stay aligned.
const promoteCanonicalThreshold = 3

// rolesComposite is the view-model for the /roles page.
type rolesComposite struct {
	Rows []roleRow `json:"rows"`
}

// agentsComposite is the view-model for the /agents page.
type agentsComposite struct {
	Rows         []agentRow    `json:"rows"`
	Filter       agentFilter   `json:"filter"`
	PromoteHints []promoteHint `json:"promote_hints,omitempty"`
}

// agentFilter carries the `?canonical=` query state. Empty = unfiltered;
// "true" = only canonical (non-ephemeral); "false" = only ephemeral.
type agentFilter struct {
	Canonical string `json:"canonical,omitempty"`
}

// promoteHint surfaces a sorted skill_refs set used by ≥ threshold
// ephemeral agents. The /agents page renders one CTA per hint.
type promoteHint struct {
	SkillRefs []string `json:"skill_refs"`
	Count     int      `json:"count"`
	Href      string   `json:"href"`
}

// grantsComposite is the view-model for the /grants page.
type grantsComposite struct {
	Rows    []grantRow `json:"rows"`
	IsAdmin bool       `json:"is_admin"`
}

type roleRow struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Scope           string   `json:"scope"`
	ActiveGrants    int      `json:"active_grants"`
	AllowedMcpVerbs []string `json:"allowed_mcp_verbs,omitempty"`
	RequiredHooks   []string `json:"required_hooks,omitempty"`
}

type agentRow struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Scope              string   `json:"scope"`
	ProviderChain      []string `json:"provider_chain,omitempty"`
	Tier               string   `json:"tier,omitempty"`
	PermittedRoles     []string `json:"permitted_roles,omitempty"`
	ContractBinding    string   `json:"contract_binding,omitempty"`
	PermissionPatterns []string `json:"permission_patterns,omitempty"`
	SkillRefs          []string `json:"skill_refs,omitempty"`
	Ephemeral          bool     `json:"ephemeral"`
	Canonical          bool     `json:"canonical"`
	OwningStoryID      string   `json:"owning_story_id,omitempty"`
	OwningStoryHref    string   `json:"owning_story_href,omitempty"`
	OwningProjectID    string   `json:"owning_project_id,omitempty"`
}

type grantRow struct {
	ID          string `json:"id"`
	RoleID      string `json:"role_id"`
	RoleName    string `json:"role_name,omitempty"`
	AgentID     string `json:"agent_id"`
	AgentName   string `json:"agent_name,omitempty"`
	GranteeKind string `json:"grantee_kind"`
	GranteeID   string `json:"grantee_id"`
	Status      string `json:"status"`
	IssuedAt    string `json:"issued_at"`
	ReleasedAt  string `json:"released_at,omitempty"`
}

// buildRolesComposite reads role documents + counts active grants per
// role.
func buildRolesComposite(ctx context.Context, docs document.Store, grants rolegrant.Store, memberships []string) rolesComposite {
	if docs == nil {
		return rolesComposite{}
	}
	roles, err := docs.List(ctx, document.ListOptions{Type: "role", Limit: 200}, memberships)
	if err != nil {
		return rolesComposite{}
	}
	out := make([]roleRow, 0, len(roles))
	for _, r := range roles {
		row := roleRow{
			ID:    r.ID,
			Name:  r.Name,
			Scope: r.Scope,
		}
		if grants != nil {
			active, _ := grants.List(ctx, rolegrant.ListOptions{RoleID: r.ID, Status: rolegrant.StatusActive, Limit: 500}, memberships)
			row.ActiveGrants = len(active)
		}
		var payload struct {
			AllowedMcpVerbs []string `json:"allowed_mcp_verbs"`
			RequiredHooks   []string `json:"required_hooks"`
		}
		if len(r.Structured) > 0 {
			_ = json.Unmarshal(r.Structured, &payload)
			row.AllowedMcpVerbs = payload.AllowedMcpVerbs
			row.RequiredHooks = payload.RequiredHooks
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return rolesComposite{Rows: out}
}

// parseAgentFilter reads the `?canonical=` query param into an
// agentFilter. Accepts "true" / "false"; any other value is treated as
// unfiltered.
func parseAgentFilter(r *http.Request) agentFilter {
	v := strings.TrimSpace(r.URL.Query().Get("canonical"))
	switch v {
	case "true", "false":
		return agentFilter{Canonical: v}
	default:
		return agentFilter{}
	}
}

// buildAgentsComposite reads agent documents and projects them into the
// /agents view-model. Story_7b77ffb0 surfaces the v4 typed-agent
// fields (permission_patterns, skill_refs, ephemeral, owning story)
// alongside the legacy orchestrator fields.
func buildAgentsComposite(ctx context.Context, docs document.Store, memberships []string, filter agentFilter) agentsComposite {
	if docs == nil {
		return agentsComposite{Filter: filter}
	}
	rows, err := docs.List(ctx, document.ListOptions{Type: "agent", Limit: 200}, memberships)
	if err != nil {
		return agentsComposite{Filter: filter}
	}
	out := make([]agentRow, 0, len(rows))
	for _, r := range rows {
		row := agentRow{
			ID:    r.ID,
			Name:  r.Name,
			Scope: r.Scope,
		}
		if r.ContractBinding != nil {
			row.ContractBinding = *r.ContractBinding
		}
		// Legacy orchestrator-agent fields (provider_chain / tier /
		// permitted_roles) coexist with v4 AgentSettings on the same
		// Structured payload (validateAgentStructured tolerates extras).
		var legacy struct {
			ProviderChain  []string `json:"provider_chain"`
			Tier           string   `json:"tier"`
			PermittedRoles []string `json:"permitted_roles"`
		}
		if len(r.Structured) > 0 {
			_ = json.Unmarshal(r.Structured, &legacy)
			row.ProviderChain = legacy.ProviderChain
			row.Tier = legacy.Tier
			row.PermittedRoles = legacy.PermittedRoles
		}
		settings, err := document.UnmarshalAgentSettings(r.Structured)
		if err == nil {
			row.PermissionPatterns = settings.PermissionPatterns
			row.SkillRefs = settings.SkillRefs
			row.Ephemeral = settings.Ephemeral
			if settings.StoryID != nil {
				row.OwningStoryID = *settings.StoryID
				if r.ProjectID != nil && *r.ProjectID != "" {
					row.OwningProjectID = *r.ProjectID
					row.OwningStoryHref = "/projects/" + *r.ProjectID + "/stories/" + *settings.StoryID
				}
			}
		}
		row.Canonical = !row.Ephemeral
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	hints := summariseEphemeralAgents(out)

	if filter.Canonical != "" {
		filtered := make([]agentRow, 0, len(out))
		for _, row := range out {
			switch filter.Canonical {
			case "true":
				if row.Canonical {
					filtered = append(filtered, row)
				}
			case "false":
				if row.Ephemeral {
					filtered = append(filtered, row)
				}
			}
		}
		out = filtered
	}
	return agentsComposite{Rows: out, Filter: filter, PromoteHints: hints}
}

// summariseEphemeralAgents groups ephemeral agents by their sorted
// skill_refs slice and returns one promoteHint per group whose count is
// at or above promoteCanonicalThreshold. Mirrors the substrate's
// handleAgentEphemeralSummary so the portal does not need to round-trip
// through MCP.
func summariseEphemeralAgents(rows []agentRow) []promoteHint {
	groups := make(map[string]*promoteHint)
	for _, row := range rows {
		if !row.Ephemeral {
			continue
		}
		skills := append([]string(nil), row.SkillRefs...)
		sort.Strings(skills)
		key := strings.Join(skills, ",")
		if g, ok := groups[key]; ok {
			g.Count++
			continue
		}
		groups[key] = &promoteHint{SkillRefs: skills, Count: 1}
	}
	out := make([]promoteHint, 0, len(groups))
	for _, g := range groups {
		if g.Count < promoteCanonicalThreshold {
			continue
		}
		hint := *g
		// Pre-build the documents-browser href so the template can
		// render the CTA without re-encoding.
		if len(hint.SkillRefs) > 0 {
			hint.Href = "/documents?type=skill&ids=" + strings.Join(hint.SkillRefs, ",")
		} else {
			hint.Href = "/documents?type=skill"
		}
		out = append(out, hint)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return strings.Join(out[i].SkillRefs, ",") < strings.Join(out[j].SkillRefs, ",")
	})
	return out
}

// buildGrantsComposite reads active role-grants and (optionally) joins
// role + agent names from the document store.
func buildGrantsComposite(ctx context.Context, grants rolegrant.Store, docs document.Store, memberships []string, isAdmin bool) grantsComposite {
	out := grantsComposite{IsAdmin: isAdmin}
	if grants == nil {
		return out
	}
	rows, err := grants.List(ctx, rolegrant.ListOptions{Status: rolegrant.StatusActive, Limit: 500}, memberships)
	if err != nil {
		return out
	}
	cache := map[string]string{}
	resolveName := func(id string) string {
		if id == "" {
			return ""
		}
		if v, ok := cache[id]; ok {
			return v
		}
		if docs == nil {
			cache[id] = ""
			return ""
		}
		d, err := docs.GetByID(ctx, id, memberships)
		if err != nil {
			cache[id] = ""
			return ""
		}
		cache[id] = d.Name
		return d.Name
	}
	cards := make([]grantRow, 0, len(rows))
	for _, g := range rows {
		card := grantRow{
			ID:          g.ID,
			RoleID:      g.RoleID,
			AgentID:     g.AgentID,
			GranteeKind: g.GranteeKind,
			GranteeID:   g.GranteeID,
			Status:      g.Status,
			IssuedAt:    g.IssuedAt.UTC().Format(time.RFC3339),
			RoleName:    resolveName(g.RoleID),
			AgentName:   resolveName(g.AgentID),
		}
		if g.ReleasedAt != nil {
			card.ReleasedAt = g.ReleasedAt.UTC().Format(time.RFC3339)
		}
		cards = append(cards, card)
	}
	out.Rows = cards
	return out
}
