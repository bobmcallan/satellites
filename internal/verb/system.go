package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// processStartedAt is captured once at package init — the process boot
// time. uptime_seconds is derived from it at call time, so the value is
// stable across the instance's lifetime (sty_a847fd3f).
var processStartedAt = time.Now().UTC()

// systemDBPingTimeout bounds the single round-trip DB health probe so a
// hung substrate connection can't stall the status call.
const systemDBPingTimeout = 2 * time.Second

// SystemStatusRequest is the input for system_status. client_version is
// optional and echoed back only when supplied — there is no version-skew
// logic here; the caller compares the two versions itself.
type SystemStatusRequest struct {
	ClientVersion string `json:"client_version,omitempty"`
}

type systemServerInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at"`
}

type systemClientInfo struct {
	Version string `json:"version"`
}

type systemDBHealth struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// SystemStatusResponse is the read-only health/identity payload: who the
// server is, whether the substrate DB is reachable, and how long this
// instance has been up.
type SystemStatusResponse struct {
	Server        systemServerInfo  `json:"server"`
	Client        *systemClientInfo `json:"client,omitempty"`
	DB            systemDBHealth    `json:"db"`
	UptimeSeconds int64             `json:"uptime_seconds"`
	StartedAt     string            `json:"started_at"`
}

func init() {
	Register(&Verb{
		Name:        "system_status",
		Description: "Read-only health/identity: server version/commit/build, substrate DB reachability, and process uptime. Optional client_version is echoed back.",
		Invoke:      invokeSystemStatus,
	})
}

func invokeSystemStatus(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req SystemStatusRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("system_status: %w: %v", ErrBadRequest, err)
		}
	}

	bi := resolveBuildInfo()
	now := time.Now().UTC()
	resp := SystemStatusResponse{
		Server: systemServerInfo{
			Version: bi.Version,
			Commit:  bi.Commit,
			BuiltAt: bi.BuildTime,
		},
		DB:            checkDBHealth(ctx),
		UptimeSeconds: int64(now.Sub(processStartedAt).Seconds()),
		StartedAt:     processStartedAt.Format(time.RFC3339),
	}
	// client_version is echo-only: present in the response iff the caller
	// supplied it; absent input is not an error.
	if req.ClientVersion != "" {
		resp.Client = &systemClientInfo{Version: req.ClientVersion}
	}
	return json.Marshal(resp)
}

// checkDBHealth runs a single bounded round-trip ping against the substrate
// DB. A nil store or a failed ping surfaces ok=false + error; the verb still
// succeeds — a health probe reports failure, it does not become one.
func checkDBHealth(ctx context.Context) systemDBHealth {
	if documentStore == nil || documentStore.DB == nil {
		return systemDBHealth{OK: false, Error: "substrate store not configured"}
	}
	pingCtx, cancel := context.WithTimeout(ctx, systemDBPingTimeout)
	defer cancel()
	start := time.Now()
	err := documentStore.DB.PingContext(pingCtx)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return systemDBHealth{OK: false, LatencyMS: latency, Error: err.Error()}
	}
	return systemDBHealth{OK: true, LatencyMS: latency}
}
