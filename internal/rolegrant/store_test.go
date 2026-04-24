package rolegrant_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/rolegrant"
)

// seedAgentAndRole creates one active agent doc and one active role doc
// in docs and returns their IDs. Tests share this helper so the FK
// resolution path is exercised consistently.
func seedAgentAndRole(t *testing.T, docs document.Store) (string, string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	agent, err := docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_a",
		ProjectID:   nil,
		Type:        document.TypeAgent,
		Name:        "agent_test",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
	}, now)
	require.NoError(t, err)
	role, err := docs.Create(ctx, document.Document{
		WorkspaceID: "wksp_a",
		ProjectID:   nil,
		Type:        document.TypeRole,
		Name:        "role_test",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
	}, now)
	require.NoError(t, err)
	return agent.ID, role.ID
}

func TestValidate(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	tests := []struct {
		name    string
		grant   rolegrant.RoleGrant
		wantErr bool
	}{
		{
			name: "valid active",
			grant: rolegrant.RoleGrant{
				WorkspaceID: "wksp_a",
				RoleID:      "role_x",
				AgentID:     "agent_y",
				GranteeKind: rolegrant.GranteeSession,
				GranteeID:   "session_z",
				Status:      rolegrant.StatusActive,
				IssuedAt:    now,
			},
		},
		{
			name: "valid released",
			grant: rolegrant.RoleGrant{
				WorkspaceID: "wksp_a",
				RoleID:      "role_x",
				AgentID:     "agent_y",
				GranteeKind: rolegrant.GranteeSession,
				GranteeID:   "session_z",
				Status:      rolegrant.StatusReleased,
				IssuedAt:    now,
				ReleasedAt:  timePtr(now),
			},
		},
		{
			name: "missing workspace_id",
			grant: rolegrant.RoleGrant{
				RoleID:      "role_x",
				AgentID:     "agent_y",
				GranteeKind: rolegrant.GranteeSession,
				GranteeID:   "session_z",
				Status:      rolegrant.StatusActive,
			},
			wantErr: true,
		},
		{
			name: "bad grantee_kind",
			grant: rolegrant.RoleGrant{
				WorkspaceID: "wksp_a",
				RoleID:      "role_x",
				AgentID:     "agent_y",
				GranteeKind: "squirrel",
				GranteeID:   "session_z",
				Status:      rolegrant.StatusActive,
			},
			wantErr: true,
		},
		{
			name: "released without released_at",
			grant: rolegrant.RoleGrant{
				WorkspaceID: "wksp_a",
				RoleID:      "role_x",
				AgentID:     "agent_y",
				GranteeKind: rolegrant.GranteeSession,
				GranteeID:   "session_z",
				Status:      rolegrant.StatusReleased,
			},
			wantErr: true,
		},
		{
			name: "active with released_at",
			grant: rolegrant.RoleGrant{
				WorkspaceID: "wksp_a",
				RoleID:      "role_x",
				AgentID:     "agent_y",
				GranteeKind: rolegrant.GranteeSession,
				GranteeID:   "session_z",
				Status:      rolegrant.StatusActive,
				ReleasedAt:  timePtr(now),
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.grant.Validate()
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestMemoryStoreCreate_ActiveGrant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now().UTC()
	docs := document.NewMemoryStore()
	agentID, roleID := seedAgentAndRole(t, docs)
	store := rolegrant.NewMemoryStore(docs)

	g, err := store.Create(ctx, rolegrant.RoleGrant{
		WorkspaceID: "wksp_a",
		RoleID:      roleID,
		AgentID:     agentID,
		GranteeKind: rolegrant.GranteeSession,
		GranteeID:   "session_1",
	}, now)
	require.NoError(t, err)
	assert.NotEmpty(t, g.ID)
	assert.Equal(t, rolegrant.StatusActive, g.Status)
	assert.Equal(t, now, g.IssuedAt)
	assert.Nil(t, g.ReleasedAt)

	got, err := store.GetByID(ctx, g.ID, []string{"wksp_a"})
	require.NoError(t, err)
	assert.Equal(t, g.ID, got.ID)
	assert.Equal(t, rolegrant.StatusActive, got.Status)
}

func TestMemoryStoreCreate_FKIntegrity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now().UTC()
	docs := document.NewMemoryStore()
	agentID, _ := seedAgentAndRole(t, docs)
	store := rolegrant.NewMemoryStore(docs)

	_, err := store.Create(ctx, rolegrant.RoleGrant{
		WorkspaceID: "wksp_a",
		RoleID:      "role_does_not_exist",
		AgentID:     agentID,
		GranteeKind: rolegrant.GranteeSession,
		GranteeID:   "session_1",
	}, now)
	assert.ErrorIs(t, err, rolegrant.ErrDanglingRole)

	_, roleID := seedAgentAndRole(t, docs)
	_, err = store.Create(ctx, rolegrant.RoleGrant{
		WorkspaceID: "wksp_a",
		RoleID:      roleID,
		AgentID:     "agent_does_not_exist",
		GranteeKind: rolegrant.GranteeSession,
		GranteeID:   "session_1",
	}, now)
	assert.ErrorIs(t, err, rolegrant.ErrDanglingAgent)
}

func TestMemoryStoreRelease_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now().UTC()
	docs := document.NewMemoryStore()
	agentID, roleID := seedAgentAndRole(t, docs)
	store := rolegrant.NewMemoryStore(docs)
	g, err := store.Create(ctx, rolegrant.RoleGrant{
		WorkspaceID: "wksp_a",
		RoleID:      roleID,
		AgentID:     agentID,
		GranteeKind: rolegrant.GranteeSession,
		GranteeID:   "session_1",
	}, now)
	require.NoError(t, err)

	laterNow := now.Add(time.Minute)
	released, err := store.Release(ctx, g.ID, "task_close", laterNow, []string{"wksp_a"})
	require.NoError(t, err)
	assert.Equal(t, rolegrant.StatusReleased, released.Status)
	require.NotNil(t, released.ReleasedAt)
	assert.Equal(t, laterNow, *released.ReleasedAt)
	assert.Equal(t, "task_close", released.ReleaseNote)

	// Second release: ErrAlreadyReleased with the grant row still returned.
	evenLater := laterNow.Add(time.Minute)
	again, err := store.Release(ctx, g.ID, "redundant", evenLater, []string{"wksp_a"})
	assert.ErrorIs(t, err, rolegrant.ErrAlreadyReleased)
	assert.Equal(t, released.ID, again.ID)
	assert.Equal(t, rolegrant.StatusReleased, again.Status)
	// ReleasedAt preserved from the first release — not overwritten.
	require.NotNil(t, again.ReleasedAt)
	assert.Equal(t, laterNow, *again.ReleasedAt)
}

func TestMemoryStoreConcurrentGrants(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now().UTC()
	docs := document.NewMemoryStore()
	agentID, roleID := seedAgentAndRole(t, docs)
	store := rolegrant.NewMemoryStore(docs)

	// Two sessions in the same workspace claim the same role under the
	// same agent. Both grants must coexist with distinct ids and
	// grantee_ids — the shape that resolves v3's multi-session
	// session_id singleton collision.
	g1, err := store.Create(ctx, rolegrant.RoleGrant{
		WorkspaceID: "wksp_a",
		RoleID:      roleID,
		AgentID:     agentID,
		GranteeKind: rolegrant.GranteeSession,
		GranteeID:   "session_1",
	}, now)
	require.NoError(t, err)
	g2, err := store.Create(ctx, rolegrant.RoleGrant{
		WorkspaceID: "wksp_a",
		RoleID:      roleID,
		AgentID:     agentID,
		GranteeKind: rolegrant.GranteeSession,
		GranteeID:   "session_2",
	}, now.Add(time.Second))
	require.NoError(t, err)

	assert.NotEqual(t, g1.ID, g2.ID)

	active, err := store.List(ctx, rolegrant.ListOptions{
		RoleID: roleID,
		Status: rolegrant.StatusActive,
	}, []string{"wksp_a"})
	require.NoError(t, err)
	assert.Len(t, active, 2, "two active grants should coexist per role per workspace")

	// Release one; the other must remain active.
	_, err = store.Release(ctx, g1.ID, "session_end", now.Add(2*time.Second), []string{"wksp_a"})
	require.NoError(t, err)

	active, err = store.List(ctx, rolegrant.ListOptions{
		RoleID: roleID,
		Status: rolegrant.StatusActive,
	}, []string{"wksp_a"})
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, g2.ID, active[0].ID)
}

func TestMemoryStoreWorkspaceIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now().UTC()
	docs := document.NewMemoryStore()
	agentID, roleID := seedAgentAndRole(t, docs)
	store := rolegrant.NewMemoryStore(docs)

	g, err := store.Create(ctx, rolegrant.RoleGrant{
		WorkspaceID: "wksp_a",
		RoleID:      roleID,
		AgentID:     agentID,
		GranteeKind: rolegrant.GranteeSession,
		GranteeID:   "session_1",
	}, now)
	require.NoError(t, err)

	_, err = store.GetByID(ctx, g.ID, []string{"wksp_b"})
	assert.ErrorIs(t, err, rolegrant.ErrNotFound)

	list, err := store.List(ctx, rolegrant.ListOptions{}, []string{"wksp_b"})
	require.NoError(t, err)
	assert.Empty(t, list)
}

func timePtr(t time.Time) *time.Time { return &t }
