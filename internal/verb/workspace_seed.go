// workspace_seed_apply — full-replace a workspace's seed body from the
// consumer repo's `.satellites/seeds/<workspace_id>/workspace.md`.
//
// CLI-local invocations (no authStore wired) skip the membership check;
// the HTTP/MCP path requires the caller to be a member of the target
// workspace. Idempotency lives at the store layer — re-applying the
// same body returns Applied=false with zero writes.

package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// WorkspaceSeedApplyRequest carries the target workspace id and the
// markdown body to apply. Full replace — no patching semantics.
type WorkspaceSeedApplyRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Body        string `json:"body"`
}

// WorkspaceSeedApplyResponse reports what changed. Applied=false +
// Reason="no change" is the idempotent re-push path.
type WorkspaceSeedApplyResponse struct {
	Workspace workspace.Workspace `json:"workspace"`
	Applied   bool                `json:"applied"`
	Reason    string              `json:"reason,omitempty"`
}

func init() {
	Register(&Verb{
		Name:        "workspace_seed_apply",
		Description: "Replace a workspace's seed_md with the supplied body (idempotent — no-op when bytes match).",
		Invoke:      invokeWorkspaceSeedApply,
	})
}

func invokeWorkspaceSeedApply(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if workspaceStore == nil {
		return nil, fmt.Errorf("workspace_seed_apply: store not configured")
	}
	var req WorkspaceSeedApplyRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_seed_apply: %w: %v", ErrBadRequest, err)
		}
	}
	wsID := strings.TrimSpace(req.WorkspaceID)
	if wsID == "" {
		return nil, fmt.Errorf("workspace_seed_apply: %w: workspace_id required", ErrBadRequest)
	}
	if err := authorizeWorkspaceWrite(ctx, wsID); err != nil {
		return nil, err
	}
	w, changed, err := workspaceStore.ApplySeed(ctx, wsID, req.Body, time.Now().UTC())
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, fmt.Errorf("workspace_seed_apply: %w: %s", ErrNotFound, wsID)
		}
		return nil, err
	}
	resp := WorkspaceSeedApplyResponse{Workspace: w, Applied: changed}
	if !changed {
		resp.Reason = "no change"
	}
	return json.Marshal(resp)
}

// authorizeWorkspaceWrite enforces the same membership-required contract
// document_upsert uses for workspace-scope writes. CLI-local in-process
// callers (authStore unwired) bypass; HTTP/MCP callers must be a member.
func authorizeWorkspaceWrite(ctx context.Context, wsID string) error {
	if authStore == nil {
		return nil
	}
	u := auth.FromContext(ctx)
	if u == nil {
		return fmt.Errorf("workspace seed: %w: bearer required", ErrUnauthorized)
	}
	if workspaceStore == nil {
		return nil
	}
	if _, err := workspaceStore.GetRole(ctx, wsID, u.ID); err != nil {
		if errors.Is(err, workspace.ErrMemberNotFound) {
			return fmt.Errorf("workspace seed: %w: user not a member of workspace %s", ErrForbidden, wsID)
		}
		return err
	}
	return nil
}
