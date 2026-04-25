// Roles + agents + grants browser composites for slice 6.7
// (story_5cc349a9). Three pages share this file: roles index, agents
// index, live grants panel. Per docs/ui-design.md#roles.
package portal

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/rolegrant"
)

// rolesComposite is the view-model for the /roles page.
type rolesComposite struct {
	Rows []roleRow `json:"rows"`
}

// agentsComposite is the view-model for the /agents page.
type agentsComposite struct {
	Rows []agentRow `json:"rows"`
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
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Scope           string   `json:"scope"`
	ProviderChain   []string `json:"provider_chain,omitempty"`
	Tier            string   `json:"tier,omitempty"`
	PermittedRoles  []string `json:"permitted_roles,omitempty"`
	ContractBinding string   `json:"contract_binding,omitempty"`
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

// buildAgentsComposite reads agent documents.
func buildAgentsComposite(ctx context.Context, docs document.Store, memberships []string) agentsComposite {
	if docs == nil {
		return agentsComposite{}
	}
	rows, err := docs.List(ctx, document.ListOptions{Type: "agent", Limit: 200}, memberships)
	if err != nil {
		return agentsComposite{}
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
		var payload struct {
			ProviderChain  []string `json:"provider_chain"`
			Tier           string   `json:"tier"`
			PermittedRoles []string `json:"permitted_roles"`
		}
		if len(r.Structured) > 0 {
			_ = json.Unmarshal(r.Structured, &payload)
			row.ProviderChain = payload.ProviderChain
			row.Tier = payload.Tier
			row.PermittedRoles = payload.PermittedRoles
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return agentsComposite{Rows: out}
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
