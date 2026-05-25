// project_seed_apply — full-replace a project's seed body from the
// consumer repo's `.satellites/seeds/<workspace_id>/<project_id>/project.md`.
//
// CLI-local invocations (no authStore wired) skip the membership check;
// the HTTP/MCP path requires the caller to be a member of the project's
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

	"github.com/bobmcallan/satellites/internal/project"
)

// ProjectSeedApplyRequest carries the target project id and the
// markdown body to apply. Full replace — no patching semantics.
type ProjectSeedApplyRequest struct {
	ProjectID string `json:"project_id"`
	Body      string `json:"body"`
}

// ProjectSeedApplyResponse reports what changed. Applied=false +
// Reason="no change" is the idempotent re-push path.
type ProjectSeedApplyResponse struct {
	Project project.Project `json:"project"`
	Applied bool            `json:"applied"`
	Reason  string          `json:"reason,omitempty"`
}

func init() {
	Register(&Verb{
		Name:        "project_seed_apply",
		Description: "Replace a project's seed_md with the supplied body (idempotent — no-op when bytes match).",
		Invoke:      invokeProjectSeedApply,
	})
}

func invokeProjectSeedApply(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if projectStore == nil {
		return nil, fmt.Errorf("project_seed_apply: store not configured")
	}
	var req ProjectSeedApplyRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("project_seed_apply: %w: %v", ErrBadRequest, err)
		}
	}
	pjID := strings.TrimSpace(req.ProjectID)
	if pjID == "" {
		return nil, fmt.Errorf("project_seed_apply: %w: project_id required", ErrBadRequest)
	}
	// Resolve workspace_id from the project, then authorize against it.
	p, err := projectStore.GetByID(ctx, pjID)
	if err != nil {
		if errors.Is(err, project.ErrNotFound) {
			return nil, fmt.Errorf("project_seed_apply: %w: %s", ErrNotFound, pjID)
		}
		return nil, err
	}
	if err := authorizeWorkspaceWrite(ctx, p.WorkspaceID); err != nil {
		return nil, err
	}
	updated, changed, err := projectStore.ApplySeed(ctx, pjID, req.Body, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	resp := ProjectSeedApplyResponse{Project: updated, Applied: changed}
	if !changed {
		resp.Reason = "no change"
	}
	return json.Marshal(resp)
}
