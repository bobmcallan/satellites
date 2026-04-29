package mcpserver

import (
	"context"
	"fmt"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
)

// loadResolvedWorkflowSpec walks the additive scope chain
// system → workspace → project → user defined by the design doc
// `docs/architecture-orchestrator-driven-configuration.md` §1 and
// returns the merged WorkflowSpec via contract.MergeSlots.
//
// When no document exists at any scope the loader falls back to the
// pre-existing project KV row (key:workflow_spec); when that is also
// missing it returns contract.DefaultWorkflowSpec(). Story_f0a78759.
func (s *Server) loadResolvedWorkflowSpec(ctx context.Context, workspaceID, projectID, userID string, memberships []string) (contract.WorkflowSpec, error) {
	systemSlots := s.loadWorkflowSlots(ctx, document.ScopeSystem, "", "", memberships)
	workspaceSlots := s.loadWorkflowSlots(ctx, document.ScopeWorkspace, "", "", memberships)
	projectSlots := s.loadWorkflowSlots(ctx, document.ScopeProject, projectID, "", memberships)
	userSlots := s.loadWorkflowSlots(ctx, document.ScopeUser, "", userID, memberships)

	hasAny := len(systemSlots)+len(workspaceSlots)+len(projectSlots)+len(userSlots) > 0
	if !hasAny {
		// Fall back to the project KV row + DefaultWorkflowSpec for
		// projects that have not yet adopted workflow markdowns.
		return s.loadWorkflowSpec(ctx, projectID, memberships)
	}

	merged := contract.MergeSlots(
		contract.LayerSlots{Source: contract.SourceSystem, Slots: systemSlots},
		contract.LayerSlots{Source: contract.SourceWorkspace, Slots: workspaceSlots},
		contract.LayerSlots{Source: contract.SourceProject, Slots: projectSlots},
		contract.LayerSlots{Source: contract.SourceUser, Slots: userSlots},
	)
	_ = workspaceID
	return merged, nil
}

// loadWorkflowSlots reads every type=workflow document at the given
// scope tier (filtered by projectID for scope=project, by userID for
// scope=user) and unions their required_slots into a single Slot list.
// Documents whose Structured payload fails to parse are skipped.
//
// scope=system reads with nil memberships per pr_0779e5af — system
// docs are globally readable inside the workspace. The other tiers
// are workspace-scoped via the supplied memberships slice.
func (s *Server) loadWorkflowSlots(ctx context.Context, scope, projectID, userID string, memberships []string) []contract.Slot {
	if s.docs == nil {
		return nil
	}
	opts := document.ListOptions{Type: document.TypeWorkflow, Scope: scope}
	if scope == document.ScopeProject {
		opts.ProjectID = projectID
	}
	listMemberships := memberships
	if scope == document.ScopeSystem {
		listMemberships = nil
	}
	docs, err := s.docs.List(ctx, opts, listMemberships)
	if err != nil {
		return nil
	}
	out := make([]contract.Slot, 0)
	for _, d := range docs {
		if d.Status != document.StatusActive {
			continue
		}
		if scope == document.ScopeUser && (userID == "" || d.CreatedBy != userID) {
			continue
		}
		out = append(out, contract.SlotsFromWorkflowDocStructured(d.Structured)...)
	}
	return out
}

// resolveCallerUserID returns the caller's user_id for use as the
// user-tier resolver key. Returns empty string when the call lacks a
// user identity (server-internal callers).
func resolveCallerUserID(ctx context.Context) string {
	caller, ok := UserFrom(ctx)
	if !ok {
		return ""
	}
	return caller.UserID
}

// expandResolvedDefault produces a proposed list from the resolved
// spec. Mirrors expandDefaultProposed but consumes the resolver
// output. Used by workflow_claim when the caller passes no
// proposed_contracts. story_f0a78759.
func expandResolvedDefault(spec contract.WorkflowSpec) []string {
	return expandDefaultProposed(spec)
}

// formatResolvedSpecLog returns a compact summary of the resolved spec
// suitable for structured logs.
func formatResolvedSpecLog(spec contract.WorkflowSpec) string {
	if len(spec.Slots) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(spec.Slots))
	for _, slot := range spec.Slots {
		parts = append(parts, fmt.Sprintf("%s(src=%s,min=%d,max=%d,req=%t)",
			slot.ContractName, slot.Source, slot.MinCount, slot.MaxCount, slot.Required))
	}
	out := "["
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	out += "]"
	return out
}
