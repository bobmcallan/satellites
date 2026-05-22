package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/bobmcallan/satellites/config/seed"
	"github.com/bobmcallan/satellites/internal/auth"
)

// authStore is set by the server at boot via SetAuthStore. Verbs that
// require auth access read from this package-level variable.
// CLI-local invocations never set it — those callers see
// AuthBootstrap.Kind="auth_login" and must obtain an api-key out of band.
var authStore *auth.Store

// SetAuthStore wires the server's auth.Store into the verb package so
// satellites_init can mint api-keys. Called by cmd/satellites-server on
// boot.
func SetAuthStore(s *auth.Store) { authStore = s }

// SatellitesInitRequest is the input shape for the satellites_init verb.
type SatellitesInitRequest struct {
	CurrentVersion string `json:"current_version,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	AgentName      string `json:"agent_name,omitempty"`
	OS             string `json:"os,omitempty"`
	Arch           string `json:"arch,omitempty"`
}

// SatellitesInitResponse is the install payload returned by the verb.
type SatellitesInitResponse struct {
	State             string             `json:"state"`
	Install           *InstallInfo       `json:"install,omitempty"`
	TargetInstallPath string             `json:"target_install_path"`
	TargetConfigPath  string             `json:"target_config_path"`
	DefaultConfig     seed.DefaultConfig `json:"default_config"`
	AuthBootstrap     *AuthBootstrap     `json:"auth_bootstrap,omitempty"`
}

// InstallInfo carries download + integrity info for the platform binary.
type InstallInfo struct {
	DownloadURL string `json:"download_url"`
	SHA256URL   string `json:"sha256_url,omitempty"`
}

// AuthBootstrap is the agent-API-key handoff block. Kind="ready" means
// the api-key is present in this response; Kind="auth_login" means the
// caller must authenticate first via the schema-defined command.
type AuthBootstrap struct {
	Kind     string `json:"kind"`
	Command  string `json:"command,omitempty"`
	EnvHint  string `json:"env_hint,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	APIKeyID string `json:"api_key_id,omitempty"`
}

// releaseBaseURL points at GitHub's "latest release" alias so this
// verb keeps working across tag pushes without code changes. The
// install/config paths now come from the embedded install-schema
// markdown via seed.ClientInstallSchema().
const releaseBaseURL = "https://github.com/bobmcallan/satellites/releases/latest/download"

func init() {
	Register(&Verb{
		Name:        "satellites_init",
		Description: "Return install/refresh payload + bootstrap api-key for the caller",
		Invoke:      invokeSatellitesInit,
	})
}

func invokeSatellitesInit(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req SatellitesInitRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("satellites_init: bad request: %w", err)
		}
	}
	if req.OS == "" {
		req.OS = runtime.GOOS
	}
	if req.Arch == "" {
		req.Arch = runtime.GOARCH
	}

	schema, err := seed.ClientInstallSchema()
	if err != nil {
		return nil, fmt.Errorf("satellites_init: install schema: %w", err)
	}

	state := computeState(req.CurrentVersion, Version)

	binaryName := fmt.Sprintf("satellites-%s-%s-%s", Version, req.OS, req.Arch)
	resp := SatellitesInitResponse{
		State: state,
		Install: &InstallInfo{
			DownloadURL: fmt.Sprintf("%s/%s", releaseBaseURL, binaryName),
			SHA256URL:   fmt.Sprintf("%s/%s.sha256", releaseBaseURL, binaryName),
		},
		TargetInstallPath: schema.TargetInstallPath,
		TargetConfigPath:  schema.TargetConfigPath,
		DefaultConfig:     schema.DefaultConfig,
		AuthBootstrap:     buildAuthBootstrap(ctx, req, schema.AuthBootstrap),
	}
	return json.Marshal(resp)
}

func computeState(currentVersion, serverVersion string) string {
	switch {
	case currentVersion == "":
		return "install_required"
	case currentVersion == serverVersion:
		return "up_to_date"
	default:
		return "update_available"
	}
}

func buildAuthBootstrap(ctx context.Context, req SatellitesInitRequest, schemaAuth seed.AuthBootstrapBlock) *AuthBootstrap {
	// loginFallback is the prose-driven shape from the embedded
	// install schema. Returned when the caller is unauthenticated
	// (CLI-local or anonymous MCP session) or when api-key minting
	// fails — the operator still has a recoverable path via the
	// command the schema names.
	loginFallback := &AuthBootstrap{
		Kind:    schemaAuth.Kind,
		Command: schemaAuth.Command,
		EnvHint: schemaAuth.EnvHint,
	}
	if authStore == nil {
		return loginFallback
	}
	u := auth.FromContext(ctx)
	if u == nil {
		return loginFallback
	}

	agentName := req.AgentName
	if agentName == "" {
		agentName = "cli_default"
	}

	keyID := fmt.Sprintf("apk_init_%s", randomSuffix())
	rawKey, k, err := authStore.IssueAPIKey(ctx, keyID, u.ID, req.ProjectID, agentName)
	if err != nil {
		return loginFallback
	}
	return &AuthBootstrap{
		Kind:     "ready",
		APIKey:   rawKey,
		APIKeyID: k.ID,
	}
}

// randomSuffix returns 8 hex chars suitable for unique api-key row ids.
func randomSuffix() string {
	const hex = "0123456789abcdef"
	out := make([]byte, 8)
	for i := range out {
		out[i] = hex[idx(i)%16]
	}
	return string(out)
}

// idx draws entropy from crypto/rand via a tiny helper to avoid pulling
// math/rand into the package's read-set; deterministic-looking but
// crypto-backed.
func idx(i int) int {
	var b [1]byte
	_, _ = randRead(b[:])
	return int(b[0]) ^ i
}
