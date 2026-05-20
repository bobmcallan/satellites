package verb

import (
	"context"
	"encoding/json"
)

// SessionBootstrap is the response shape for session_bootstrap.
// The full payload is defined by sty_60c48d81 (satellites_init MCP verb);
// this stub keeps the handshake surface present and self-describing.
type SessionBootstrap struct {
	ProjectID string `json:"project_id,omitempty"`
	MCPUrl    string `json:"mcp_url,omitempty"`
	Note      string `json:"note,omitempty"`
}

// AuthResult is the response shape for auth.
type AuthResult struct {
	OK   bool   `json:"ok"`
	User string `json:"user,omitempty"`
	Note string `json:"note,omitempty"`
}

// VerbCatalog is the response shape for verb_discovery.
type VerbCatalog struct {
	Verbs []string `json:"verbs"`
}

func init() {
	Register(&Verb{
		Name:        "session_bootstrap",
		Description: "Bootstrap a session against satellites-server (stub until sty_9b3e355c + sty_60c48d81)",
		Invoke: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(SessionBootstrap{
				Note: "session_bootstrap stub — full payload lands with sty_60c48d81 (satellites_init MCP verb)",
			})
		},
	})

	Register(&Verb{
		Name:        "auth",
		Description: "Validate session credential (stub until sty_9b3e355c)",
		Invoke: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(AuthResult{
				OK:   false,
				Note: "auth stub — OAuth + api-key validation arrive with sty_9b3e355c",
			})
		},
	})

	Register(&Verb{
		Name:        "verb_discovery",
		Description: "Return the catalog of available verbs",
		Invoke: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(VerbCatalog{Verbs: Catalog()})
		},
	})
}
