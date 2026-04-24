package mechanical_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/agent/mechanical"
	"github.com/bobmcallan/satellites/internal/ledger"
)

func TestRequestValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		req     mechanical.Request
		wantErr bool
	}{
		{
			name: "valid",
			req: mechanical.Request{
				Trigger:     mechanical.TriggerNoAgent,
				WorkspaceID: "wksp_a",
				RoleID:      "role_x",
				GranteeID:   "session_z",
			},
		},
		{
			name: "bad trigger",
			req: mechanical.Request{
				Trigger:     "unknown",
				WorkspaceID: "wksp_a",
				RoleID:      "role_x",
				GranteeID:   "session_z",
			},
			wantErr: true,
		},
		{
			name: "missing workspace",
			req: mechanical.Request{
				Trigger:   mechanical.TriggerExhausted,
				RoleID:    "role_x",
				GranteeID: "session_z",
			},
			wantErr: true,
		},
		{
			name: "missing role",
			req: mechanical.Request{
				Trigger:     mechanical.TriggerNoAgent,
				WorkspaceID: "wksp_a",
				GranteeID:   "session_z",
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.req.Validate()
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// runRunner is a small helper that invokes the runner and returns the
// collected ledger rows for the caller to assert on.
func runRunner(t *testing.T, trigger mechanical.Trigger, reason string) (mechanical.Result, []ledger.LedgerEntry) {
	t.Helper()
	store := ledger.NewMemoryStore()
	r := mechanical.NewRunner(store)
	now := time.Now().UTC()
	res, err := r.Run(context.Background(), mechanical.Request{
		Trigger:        trigger,
		WorkspaceID:    "wksp_a",
		ProjectID:      "proj_a",
		RoleID:         "role_orchestrator",
		AgentID:        "agent_claude_orchestrator",
		GranteeKind:    "session",
		GranteeID:      "sess_test",
		Actor:          "user_test",
		EffectiveVerbs: []string{"document_get", "document_list"},
		Reason:         reason,
	}, now)
	require.NoError(t, err)
	rows, err := store.List(context.Background(), "proj_a", ledger.ListOptions{}, nil)
	require.NoError(t, err)
	return res, rows
}

func TestRun_NoAgentResolved_TagsProviderMechanical(t *testing.T) {
	t.Parallel()
	res, rows := runRunner(t, mechanical.TriggerNoAgent, "no agent permits role_orchestrator")
	require.Len(t, rows, 1)
	assert.Equal(t, mechanical.TriggerNoAgent, res.Trigger)
	assert.NotEmpty(t, res.LedgerRowID, "ledger row id should be stamped on result")
	assertHasTag(t, rows[0].Tags, "provider:mechanical")
	assertHasTag(t, rows[0].Tags, "kind:mechanical-run")
	assertHasTag(t, rows[0].Tags, "reason:no-agent-resolved")
}

func TestRun_ProviderExhausted_TagsProviderMechanical(t *testing.T) {
	t.Parallel()
	_, rows := runRunner(t, mechanical.TriggerExhausted, "all providers errored")
	require.Len(t, rows, 1)
	assertHasTag(t, rows[0].Tags, "provider:mechanical")
	assertHasTag(t, rows[0].Tags, "reason:provider-exhausted")
}

func TestRun_ExplicitForce_TagsProviderMechanical(t *testing.T) {
	t.Parallel()
	_, rows := runRunner(t, mechanical.TriggerForceFlag, "provider_override=mechanical")
	require.Len(t, rows, 1)
	assertHasTag(t, rows[0].Tags, "provider:mechanical")
	assertHasTag(t, rows[0].Tags, "reason:explicit-force")
}

func TestRun_NilLedger_ReturnsResultWithoutWriting(t *testing.T) {
	t.Parallel()
	r := mechanical.NewRunner(nil)
	res, err := r.Run(context.Background(), mechanical.Request{
		Trigger:     mechanical.TriggerForceFlag,
		WorkspaceID: "wksp_a",
		RoleID:      "role_x",
		GranteeKind: "session",
		GranteeID:   "sess_y",
	}, time.Now())
	require.NoError(t, err)
	assert.Empty(t, res.LedgerRowID, "no ledger wiring → no ledger row id")
	assert.Equal(t, mechanical.TriggerForceFlag, res.Trigger)
	assert.NotEmpty(t, res.Content)
}

func TestRun_InvalidRequestRejected(t *testing.T) {
	t.Parallel()
	r := mechanical.NewRunner(ledger.NewMemoryStore())
	_, err := r.Run(context.Background(), mechanical.Request{Trigger: "bogus", WorkspaceID: "w", RoleID: "r", GranteeID: "g"}, time.Now())
	assert.Error(t, err)
}

func assertHasTag(t *testing.T, tags []string, want string) {
	t.Helper()
	for _, tag := range tags {
		if tag == want {
			return
		}
	}
	t.Errorf("tag %q not found in %v", want, tags)
}
