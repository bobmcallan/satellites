package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
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

// TestSessionHeader_ServerMintsAndRoundTrips covers epic:agent-process-v1
// (sty_31975268): the Streamable HTTP server mints a Mcp-Session-Id on
// initialize, returns it via the response header, and treats subsequent
// tool calls echoing the header as the same session — no body session_id
// arg required.
//
//  1. POST initialize → response Mcp-Session-Id header carries a UUID.
//  2. POST session_register({}) with the header echoed → server uses the
//     header's id, registers it, and returns the same id in the response.
//  3. POST session_whoami({}) with the header echoed → returns the same
//     session row without ever passing session_id as a body arg.
func TestSessionHeader_ServerMintsAndRoundTrips(t *testing.T) {
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
			"SATELLITES_DB_DSN":   "ws://root:root@surrealdb:8000/rpc/satellites/satellites",
			"SATELLITES_API_KEYS": "key_hdr",
			"SATELLITES_DOCS_DIR": "/app/docs",
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

	// Step 1: initialize and capture the server-minted session id.
	initBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "header-test", "version": "0.0.1"},
		},
	})
	initReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(initBody))
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Accept", "application/json, text/event-stream")
	initReq.Header.Set("Authorization", "Bearer key_hdr")
	initResp, err := http.DefaultClient.Do(initReq)
	require.NoError(t, err)
	defer initResp.Body.Close()
	require.True(t, initResp.StatusCode == http.StatusOK || initResp.StatusCode == http.StatusAccepted,
		"initialize status = %d", initResp.StatusCode)

	sessionHeader := initResp.Header.Get("Mcp-Session-Id")
	require.NotEmpty(t, sessionHeader, "initialize response must carry Mcp-Session-Id header")

	// Step 2: session_register with the header echoed and NO body session_id.
	regResp := callToolWithSessionHeader(t, ctx, mcpURL, "key_hdr", sessionHeader, "session_register", map[string]any{})
	regSessionID, _ := regResp["session_id"].(string)
	assert.Equal(t, sessionHeader, regSessionID, "session_register must use the Mcp-Session-Id header as the registered session_id")

	// Step 3: session_whoami with the header echoed and NO body session_id.
	whoamiResp := callToolWithSessionHeader(t, ctx, mcpURL, "key_hdr", sessionHeader, "session_whoami", map[string]any{})
	whoamiSessionID, _ := whoamiResp["session_id"].(string)
	assert.Equal(t, sessionHeader, whoamiSessionID, "session_whoami must resolve the session_id from Mcp-Session-Id header")
}

// callToolWithSessionHeader posts a tools/call carrying the
// Mcp-Session-Id header and returns the parsed JSON object the handler
// emitted. Mirrors callTool but with the header set so it exercises the
// header-driven session id resolution path (story_31975268).
func callToolWithSessionHeader(t *testing.T, ctx context.Context, mcpURL, apiKey, sessionID, name string, args map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.True(t, resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted,
		"%s status = %d", name, resp.StatusCode)
	raw, _ := io.ReadAll(resp.Body)
	ct := resp.Header.Get("Content-Type")
	var rpcOut map[string]any
	if strings.HasPrefix(ct, "text/event-stream") {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "data:") {
				_ = json.Unmarshal([]byte(strings.TrimSpace(line[len("data:"):])), &rpcOut)
				break
			}
		}
	} else {
		_ = json.Unmarshal(raw, &rpcOut)
	}
	require.NotNil(t, rpcOut, "%s: no JSON-RPC response decoded; raw=%s", name, string(raw))
	result, _ := rpcOut["result"].(map[string]any)
	require.NotNil(t, result, "%s: no result on response: %+v", name, rpcOut)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("%s isError: %+v", name, result)
	}
	content, _ := result["content"].([]any)
	require.Greater(t, len(content), 0, "%s: empty content", name)
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s decode: %v; raw=%s", name, err, text)
	}
	return out
}
