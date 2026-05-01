package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/reviewer"
)

// validationModeFixture returns a minimal Server + a CI seeded in
// f.cis[0] suitable for resolveValidationMode tests. Reuses the
// claimFixture wiring (workspace, project, contract docs, ledger).
func validationModeFixture(t *testing.T) *closeFixture {
	t.Helper()
	return newCloseFixture(t)
}

// seedSystemModeRow appends a system-scope KV row carrying
// lifecycle.validation_mode = value. Mirrors the production seed in
// cmd/satellites/main.go::seedSystemValidationMode.
func seedSystemModeRow(t *testing.T, f *closeFixture, value string) {
	t.Helper()
	_, err := f.server.ledger.Append(f.ctx, ledger.LedgerEntry{
		WorkspaceID: "",
		Type:        ledger.TypeKV,
		Tags:        []string{"scope:system", "key:lifecycle.validation_mode"},
		Content:     value,
		CreatedBy:   "system",
	}, f.now)
	require.NoError(t, err)
}

func seedScopedModeRow(t *testing.T, f *closeFixture, scope, workspaceID, projectID, userID, value string) {
	t.Helper()
	tags := []string{"scope:" + scope, "key:lifecycle.validation_mode"}
	if userID != "" {
		tags = append(tags, "user:"+userID)
	}
	_, err := f.server.ledger.Append(f.ctx, ledger.LedgerEntry{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Type:        ledger.TypeKV,
		Tags:        tags,
		Content:     value,
		CreatedBy:   "system",
	}, f.now)
	require.NoError(t, err)
}

func TestResolveValidationMode_SystemDefault(t *testing.T) {
	t.Parallel()
	f := validationModeFixture(t)
	seedSystemModeRow(t, f, reviewer.ModeTask)

	mode := f.server.resolveValidationMode(f.ctx, f.cis[0], f.caller.UserID, []string{f.wsID})
	assert.Equal(t, reviewer.ModeTask, mode, "system-tier task seed should win when no override is present")
}

func TestResolveValidationMode_ProjectOverride(t *testing.T) {
	t.Parallel()
	f := validationModeFixture(t)
	seedSystemModeRow(t, f, reviewer.ModeTask)
	// Project override pins the mode to llm so a single project can
	// stay on the legacy reviewer until it migrates.
	seedScopedModeRow(t, f, "project", f.wsID, f.projectID, "", reviewer.ModeLLM)
	// Stamp the CI's project_id so the resolver's KV scope reaches
	// the project tier.
	updated := f.cis[0]
	updated.ProjectID = f.projectID
	updated.WorkspaceID = f.wsID

	mode := f.server.resolveValidationMode(f.ctx, updated, f.caller.UserID, []string{f.wsID})
	assert.Equal(t, reviewer.ModeLLM, mode, "project-tier override should beat system default")
}

func TestResolveValidationMode_UserOverride(t *testing.T) {
	t.Parallel()
	f := validationModeFixture(t)
	seedSystemModeRow(t, f, reviewer.ModeTask)
	// User override flips the mode for one developer's local debug
	// session without changing the project default.
	seedScopedModeRow(t, f, "user", f.wsID, "", f.caller.UserID, reviewer.ModeLLM)
	updated := f.cis[0]
	updated.WorkspaceID = f.wsID

	mode := f.server.resolveValidationMode(f.ctx, updated, f.caller.UserID, []string{f.wsID})
	assert.Equal(t, reviewer.ModeLLM, mode, "user-tier override should beat system default")
}

func TestResolveValidationMode_DeprecatedContractDocFallback(t *testing.T) {
	t.Parallel()
	f := validationModeFixture(t)
	// No KV row at all — resolver should fall through to the contract
	// document's structured validation_mode field. Stamp the legacy
	// shape on the CI's contract doc.
	planContractID := f.cis[0].ContractID
	doc, err := f.server.docs.GetByID(f.ctx, planContractID, nil)
	require.NoError(t, err)
	structured, _ := json.Marshal(map[string]any{
		"required_role":   "role_orchestrator",
		"validation_mode": reviewer.ModeLLM,
	})
	_, err = f.server.docs.Update(f.ctx, doc.ID, document.UpdateFields{Structured: &structured}, "test", f.now, nil)
	require.NoError(t, err)

	mode := f.server.resolveValidationMode(f.ctx, f.cis[0], f.caller.UserID, []string{f.wsID})
	assert.Equal(t, reviewer.ModeLLM, mode, "absent KV row should fall through to contract doc's validation_mode field")
}

func TestResolveValidationMode_LegacyDefault(t *testing.T) {
	t.Parallel()
	f := validationModeFixture(t)
	// No KV row, no contract doc field — backstop must be ModeTask.
	mode := f.server.resolveValidationMode(f.ctx, f.cis[0], f.caller.UserID, []string{f.wsID})
	assert.Equal(t, reviewer.ModeTask, mode, "absent KV + absent contract field should fall through to DefaultValidationMode")
}

// TestResolveValidationMode_ProjectBeatsContractDoc verifies the
// resolver's tier order — when both a project KV row and a deprecated
// contract-doc field exist, the KV row wins.
func TestResolveValidationMode_ProjectBeatsContractDoc(t *testing.T) {
	t.Parallel()
	f := validationModeFixture(t)
	// KV row says task; contract doc says llm. KV must win.
	seedScopedModeRow(t, f, "project", f.wsID, f.projectID, "", reviewer.ModeTask)
	planContractID := f.cis[0].ContractID
	doc, err := f.server.docs.GetByID(f.ctx, planContractID, nil)
	require.NoError(t, err)
	structured, _ := json.Marshal(map[string]any{
		"required_role":   "role_orchestrator",
		"validation_mode": reviewer.ModeLLM,
	})
	_, err = f.server.docs.Update(f.ctx, doc.ID, document.UpdateFields{Structured: &structured}, "test", f.now, nil)
	require.NoError(t, err)
	updated := f.cis[0]
	updated.ProjectID = f.projectID
	updated.WorkspaceID = f.wsID

	mode := f.server.resolveValidationMode(f.ctx, updated, f.caller.UserID, []string{f.wsID})
	assert.Equal(t, reviewer.ModeTask, mode, "project KV row should beat the deprecated contract-doc fallback")
}

// TestResolveValidationMode_NilLedger covers the early-boot test
// fixture path — no ledger wired at all. The resolver still returns
// the legacy default rather than panicking.
func TestResolveValidationMode_NilLedger(t *testing.T) {
	t.Parallel()
	s := &Server{}
	mode := s.resolveValidationMode(t.Context(), contract.ContractInstance{}, "u1", nil)
	assert.Equal(t, reviewer.ModeTask, mode)
}
