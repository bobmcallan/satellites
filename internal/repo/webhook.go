package repo

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ternarybob/arbor"

	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// webhook ledger row tags.
const (
	tagWebhookDelivery   = "kind:webhook-delivery"
	tagWebhookAuthFailed = "kind:webhook-auth-failed"
)

const (
	// MaxWebhookBodyBytes caps the request body size accepted by the
	// webhook receiver. GitHub's largest documented payload is ~25 MB
	// (push events with many commits); satellites only inspects a few
	// fields, so we cap at 1 MB.
	MaxWebhookBodyBytes = 1 << 20

	// HeaderGitHubDelivery is the per-delivery id GitHub provides on
	// every webhook call (UUID). Used for idempotence.
	HeaderGitHubDelivery = "X-GitHub-Delivery"
	// HeaderGitHubSignature carries the HMAC-SHA256 of the body keyed
	// by the per-repo webhook secret.
	HeaderGitHubSignature = "X-Hub-Signature-256"

	// HeaderGitLabDelivery is GitLab's analogue.
	HeaderGitLabDelivery = "X-Gitlab-Event-UUID"
	// HeaderGitLabSignature carries GitLab's per-hook secret token.
	HeaderGitLabSignature = "X-Gitlab-Token"
)

// WebhookDeps bundles the resources the receiver needs.
type WebhookDeps struct {
	Repos  Store
	Tasks  task.Store
	Ledger ledger.Store
	Logger arbor.ILogger
}

// WebhookHandler owns the POST /webhooks/git/{provider} route. It
// implements httpserver.RouteRegistrar via Register(mux).
type WebhookHandler struct {
	deps WebhookDeps
}

// NewWebhookHandler constructs a WebhookHandler.
func NewWebhookHandler(deps WebhookDeps) *WebhookHandler {
	return &WebhookHandler{deps: deps}
}

// Register installs the route on the supplied mux.
func (h *WebhookHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /webhooks/git/{provider}", h.serve)
}

// pushPayload is the minimal shape satellites cares about. Both
// GitHub and GitLab push events share these fields (different keys —
// the unmarshaller below handles both).
type pushPayload struct {
	Ref        string         `json:"ref"`
	Repository pushRepository `json:"repository"`
	After      string         `json:"after,omitempty"` // GitHub uses 'after' for the new head sha
}

type pushRepository struct {
	CloneURL      string `json:"clone_url,omitempty"`
	SSHURL        string `json:"ssh_url,omitempty"`
	GitSSHURL     string `json:"git_ssh_url,omitempty"`  // GitLab
	GitHTTPURL    string `json:"git_http_url,omitempty"` // GitLab
	DefaultBranch string `json:"default_branch,omitempty"`
}

// candidateRemotes returns every URL the payload says identifies the
// pushed repo. The receiver tries each against LookupByRemote — if any
// matches a tracked Repo row, the call proceeds.
func (p pushPayload) candidateRemotes() []string {
	out := make([]string, 0, 4)
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	add(p.Repository.CloneURL)
	add(p.Repository.SSHURL)
	add(p.Repository.GitSSHURL)
	add(p.Repository.GitHTTPURL)
	return out
}

func (h *WebhookHandler) serve(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if provider != "github" && provider != "gitlab" {
		http.Error(w, `{"error":"provider_unknown"}`, http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxWebhookBodyBytes))
	if err != nil {
		http.Error(w, `{"error":"read_body"}`, http.StatusBadRequest)
		return
	}

	var payload pushPayload
	if jerr := json.Unmarshal(body, &payload); jerr != nil {
		http.Error(w, `{"error":"invalid_json"}`, http.StatusBadRequest)
		return
	}

	deliveryID := pickDeliveryID(provider, r.Header)
	if deliveryID == "" {
		http.Error(w, `{"error":"missing_delivery_id"}`, http.StatusBadRequest)
		return
	}

	tracked := h.findTrackedRepo(r.Context(), payload.candidateRemotes())
	if tracked == nil {
		// Per AC: no audit row — avoid log amplification.
		http.Error(w, `{"error":"repo_not_tracked"}`, http.StatusNotFound)
		return
	}

	if !verifySignature(provider, r.Header, body, tracked.WebhookSecret) {
		now := time.Now().UTC()
		h.appendAuthFailedRow(r.Context(), *tracked, deliveryID, now)
		http.Error(w, `{"error":"signature_mismatch"}`, http.StatusUnauthorized)
		return
	}

	if h.deliveryAlreadySeen(r.Context(), *tracked, deliveryID) {
		writeJSON(w, http.StatusOK, map[string]any{
			"deduplicated": true,
			"delivery_id":  deliveryID,
			"repo_id":      tracked.ID,
		})
		return
	}

	if !pushedToDefaultBranch(payload, tracked.DefaultBranch) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ignored": true,
			"reason":  "non-default-branch",
			"ref":     payload.Ref,
			"repo_id": tracked.ID,
		})
		return
	}

	now := time.Now().UTC()
	h.recordDelivery(r.Context(), *tracked, deliveryID, payload.Ref, now)

	taskID := h.enqueueWebhookReindex(r.Context(), *tracked, deliveryID, now)
	if taskID == "" {
		http.Error(w, `{"error":"enqueue_failed"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"task_id":     taskID,
		"repo_id":     tracked.ID,
		"delivery_id": deliveryID,
	})
}

// findTrackedRepo probes each candidate URL against the repo store.
// Returns the first match — typically there's one tracked row per
// (workspace, remote), but LookupByRemote returns all matches across
// workspaces.
func (h *WebhookHandler) findTrackedRepo(ctx context.Context, candidates []string) *Repo {
	for _, c := range candidates {
		rows, err := h.deps.Repos.LookupByRemote(ctx, c)
		if err != nil || len(rows) == 0 {
			continue
		}
		out := rows[0]
		return &out
	}
	return nil
}

func pickDeliveryID(provider string, h http.Header) string {
	switch provider {
	case "github":
		return h.Get(HeaderGitHubDelivery)
	case "gitlab":
		return h.Get(HeaderGitLabDelivery)
	}
	return ""
}

// verifySignature checks the per-repo HMAC. GitHub uses sha256-prefixed
// hex over the body; GitLab uses a shared-secret token compared with
// constant time.
func verifySignature(provider string, h http.Header, body []byte, secret string) bool {
	if secret == "" {
		// Fail-secure: a repo without a configured secret rejects every
		// webhook.
		return false
	}
	switch provider {
	case "github":
		got := h.Get(HeaderGitHubSignature)
		if !strings.HasPrefix(got, "sha256=") {
			return false
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		return hmac.Equal([]byte(got), []byte(want))
	case "gitlab":
		got := h.Get(HeaderGitLabSignature)
		return hmac.Equal([]byte(got), []byte(secret))
	}
	return false
}

func (h *WebhookHandler) deliveryAlreadySeen(ctx context.Context, r Repo, deliveryID string) bool {
	if h.deps.Ledger == nil {
		return false
	}
	// ledger.ListOptions.Tags is OR-matched; filtering by the unique
	// delivery_id tag is precise enough since deliveryIDs come from the
	// provider and are universally unique per push.
	rows, err := h.deps.Ledger.List(ctx, r.ProjectID, ledger.ListOptions{
		Tags: []string{"delivery_id:" + deliveryID},
	}, nil)
	if err != nil {
		return false
	}
	for _, row := range rows {
		if hasTag(row.Tags, tagWebhookDelivery) {
			return true
		}
	}
	return false
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func (h *WebhookHandler) recordDelivery(ctx context.Context, r Repo, deliveryID, ref string, now time.Time) {
	if h.deps.Ledger == nil {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"delivery_id": deliveryID,
		"repo_id":     r.ID,
		"ref":         ref,
	})
	_, _ = h.deps.Ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: r.WorkspaceID,
		ProjectID:   r.ProjectID,
		Type:        ledger.TypeDecision,
		Tags: []string{
			tagWebhookDelivery,
			"repo_id:" + r.ID,
			"delivery_id:" + deliveryID,
		},
		Content:    fmt.Sprintf("webhook delivery %s for repo %s", deliveryID, r.ID),
		Structured: body,
	}, now)
}

func (h *WebhookHandler) appendAuthFailedRow(ctx context.Context, r Repo, deliveryID string, now time.Time) {
	if h.deps.Ledger == nil {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"repo_id":     r.ID,
		"delivery_id": deliveryID,
	})
	_, _ = h.deps.Ledger.Append(ctx, ledger.LedgerEntry{
		WorkspaceID: r.WorkspaceID,
		ProjectID:   r.ProjectID,
		Type:        ledger.TypeDecision,
		Tags: []string{
			tagWebhookAuthFailed,
			"repo_id:" + r.ID,
			"delivery_id:" + deliveryID,
		},
		Content:    fmt.Sprintf("webhook signature mismatch for repo %s", r.ID),
		Structured: body,
	}, now)
}

func (h *WebhookHandler) enqueueWebhookReindex(ctx context.Context, r Repo, deliveryID string, now time.Time) string {
	return enqueueReindexFromStale(ctx, h.deps.Tasks, h.deps.Ledger, r, "webhook:"+deliveryID, "", now)
}

func pushedToDefaultBranch(p pushPayload, repoDefault string) bool {
	if repoDefault == "" {
		return false
	}
	expected := "refs/heads/" + repoDefault
	return p.Ref == expected
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	b, _ := json.Marshal(body)
	_, _ = w.Write(b)
}
