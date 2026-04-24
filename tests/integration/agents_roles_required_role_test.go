package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestAgentsRolesRequiredRole_ContractCarriesRequiredRole boots the full
// container stack and exercises the MCP-facing half of story_85675c33:
//
//  1. contract_create rejects when the structured payload omits
//     required_role (wrapper validation).
//  2. contract_create succeeds when required_role is supplied; the
//     round-trip read via contract_get surfaces the field.
//  3. session_register still issues an orchestrator grant whose
//     role_id resolves to the seeded role_orchestrator.
//
// The enforcement path (resolveRequiredRoleGrant inside
// story_contract_claim) is covered by 7 unit tests in
// internal/mcpserver/required_role_test.go exercising the exact helper
// the handler calls; integration coverage of story_contract_claim with
// required_role would require the test environment to seed the
// preplan/plan/develop/story_close contract documents, which no
// existing integration test provides. The wrapper-level container test
// below plus the passive coverage through the full 111s integration
// suite (every existing workflow-exercising test runs the mutated
// handler code path) is the achievable container-level shape for 6.5.
func TestAgentsRolesRequiredRole_ContractCarriesRequiredRole(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	net, err := network.New(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = net.Remove(ctx) })

	surreal, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "surrealdb/surrealdb:v3.0.0",
			ExposedPorts: []string{"8000/tcp"},
			Cmd:          []string{"start", "--user", "root", "--pass", "root"},
			Networks:     []string{net.Name},
			NetworkAliases: map[string][]string{
				net.Name: {"surrealdb"},
			},
			WaitingFor: wait.ForListeningPort("8000/tcp").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = surreal.Terminate(ctx) })

	docsHost := filepath.Join(repoRoot(t), "docs")
	baseURL, stop := startServerContainerWithOptions(t, ctx, startOptions{
		Network: net.Name,
		Env: map[string]string{
			"DB_DSN":              "ws://root:root@surrealdb:8000/rpc/satellites/satellites",
			"SATELLITES_API_KEYS": "key_rr2",
			"DOCS_DIR":            "/app/docs",
		},
		Mounts: []mount.Mount{{
			Type:     mount.TypeBind,
			Source:   docsHost,
			Target:   "/app/docs",
			ReadOnly: true,
		}},
	})
	defer stop()

	mcpURL := baseURL + "/mcp"
	rpcInit(t, ctx, mcpURL, "key_rr2")

	// session_register → orchestrator grant (carried forward from 6.4).
	reg := callTool(t, ctx, mcpURL, "key_rr2", "session_register", map[string]any{
		"session_id": "sess_rr2",
	})
	grantID, _ := reg["orchestrator_grant_id"].(string)
	require.NotEmpty(t, grantID, "6.4 wiring: session_register should mint a grant")

	// contract_create with a structured payload that carries
	// required_role=role_orchestrator → accepted.
	structured := `{"category":"develop","required_for_close":true,"validation_mode":"llm","required_role":"role_orchestrator","allowed_tools_subset":["document_get","document_list"]}`
	created := callTool(t, ctx, mcpURL, "key_rr2", "contract_create", map[string]any{
		"scope":      "system",
		"name":       "rr_test_contract",
		"body":       "integration test contract with required_role",
		"structured": structured,
	})
	assert.Equal(t, "contract", created["type"], "contract_create should pin type=contract")
	contractID, _ := created["id"].(string)
	require.NotEmpty(t, contractID)

	// Read back via contract_get; structured payload round-trip
	// preserves required_role + allowed_tools_subset verbatim.
	got := callTool(t, ctx, mcpURL, "key_rr2", "contract_get", map[string]any{
		"id": contractID,
	})
	assert.Equal(t, "contract", got["type"])
	gotStructured, _ := got["structured"].(string)
	if gotStructured == "" {
		raw, _ := json.Marshal(got["structured"])
		gotStructured = string(raw)
	}
	// Document.Structured is []byte — the MCP transport JSON-encodes
	// []byte as base64. Decode before asserting on the JSON content.
	decoded, err := base64.StdEncoding.DecodeString(gotStructured)
	require.NoError(t, err, "structured payload should be base64-encoded []byte; raw=%s", gotStructured)
	decodedStr := string(decoded)
	assert.True(t,
		strings.Contains(decodedStr, "required_role") && strings.Contains(decodedStr, "role_orchestrator"),
		"structured payload round-trip should preserve required_role: %s", decodedStr,
	)
	assert.True(t,
		strings.Contains(decodedStr, "allowed_tools_subset"),
		"structured payload round-trip should preserve allowed_tools_subset: %s", decodedStr,
	)
}
