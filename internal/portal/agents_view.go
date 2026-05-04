package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/document"
)

const promoteCanonicalThreshold = 3

type agentsComposite struct {
	Rows         []agentRow    `json:"rows"`
	Filter       agentFilter   `json:"filter"`
	PromoteHints []promoteHint `json:"promote_hints,omitempty"`
}

type agentFilter struct {
	Canonical string `json:"canonical,omitempty"`
}

type promoteHint struct {
	SkillRefs []string `json:"skill_refs"`
	Count     int      `json:"count"`
	Href      string   `json:"href"`
}

type agentRow struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Scope              string   `json:"scope"`
	ProviderChain      []string `json:"provider_chain,omitempty"`
	Tier               string   `json:"tier,omitempty"`
	ContractBinding    string   `json:"contract_binding,omitempty"`
	PermissionPatterns []string `json:"permission_patterns,omitempty"`
	SkillRefs          []string `json:"skill_refs,omitempty"`
	Ephemeral          bool     `json:"ephemeral"`
	Canonical          bool     `json:"canonical"`
	OwningStoryID      string   `json:"owning_story_id,omitempty"`
	OwningStoryHref    string   `json:"owning_story_href,omitempty"`
	OwningProjectID    string   `json:"owning_project_id,omitempty"`
	CreatedAt          string   `json:"created_at,omitempty"`
	UpdatedAt          string   `json:"updated_at,omitempty"`
	Body               string   `json:"body,omitempty"`
}

func parseAgentFilter(r *http.Request) agentFilter {
	v := strings.TrimSpace(r.URL.Query().Get("canonical"))
	switch v {
	case "true", "false":
		return agentFilter{Canonical: v}
	default:
		return agentFilter{}
	}
}

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
			ID:        r.ID,
			Name:      r.Name,
			Scope:     r.Scope,
			Body:      r.Body,
			CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			UpdatedAt: r.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		}
		if r.ContractBinding != nil {
			row.ContractBinding = *r.ContractBinding
		}
		var legacy struct {
			ProviderChain []string `json:"provider_chain"`
			Tier          string   `json:"tier"`
		}
		if len(r.Structured) > 0 {
			_ = json.Unmarshal(r.Structured, &legacy)
			row.ProviderChain = legacy.ProviderChain
			row.Tier = legacy.Tier
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
