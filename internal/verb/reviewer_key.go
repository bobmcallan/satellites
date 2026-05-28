// Server-internal reviewer-key mint/revoke wrapper.
//
// sty_25d5b21e: the substrate must be able to mint a short-lived
// reviewer key for the review subprocess, and revoke it once review
// completes. Both events are logged to the target story's ledger so
// the audit trail of who-touched-status is auditable end-to-end.
//
// auth.Store owns the key lifecycle; this file is the thin verb-layer
// shim that pairs each mint/revoke with the matching ledger row. Kept
// out of auth so the auth package stays free of a ledger dependency.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// MintReviewerKeyInput is the input to MintReviewerKeyForStory. StoryID
// is required: every mint has to be attributable to a specific review.
// TTL falls back to auth.Store's 5-minute default when zero.
type MintReviewerKeyInput struct {
	StoryID   string
	UserID    string
	ProjectID string
	AgentName string
	TTL       time.Duration
}

// MintReviewerKeyResponse mirrors apikey_create's wire shape so future
// callers can swap mint paths without parsing differently.
type MintReviewerKeyResponse struct {
	APIKey    string     `json:"apikey"`
	KeyID     string     `json:"key_id"`
	StoryID   string     `json:"story_id"`
	ProjectID string     `json:"project_id,omitempty"`
	AgentName string     `json:"agent_name,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// MintReviewerKeyForStory mints a reviewer-role key and appends a
// `reviewer_key_minted` row to the story's ledger. Server-internal —
// not registered as a verb; callers pull it in directly from the
// review subprocess (story 6).
func MintReviewerKeyForStory(ctx context.Context, in MintReviewerKeyInput) (*MintReviewerKeyResponse, error) {
	if authStore == nil {
		return nil, fmt.Errorf("mint reviewer key: auth store not configured")
	}
	if ledgerStore == nil {
		return nil, fmt.Errorf("mint reviewer key: ledger store not configured")
	}
	if strings.TrimSpace(in.StoryID) == "" {
		return nil, fmt.Errorf("mint reviewer key: story_id required")
	}
	if strings.TrimSpace(in.UserID) == "" {
		return nil, fmt.Errorf("mint reviewer key: user_id required")
	}

	rawKey, key, err := authStore.IssueReviewerKey(ctx, auth.IssueReviewerKeyInput{
		UserID:    in.UserID,
		ProjectID: in.ProjectID,
		AgentName: in.AgentName,
		TTL:       in.TTL,
	})
	if err != nil {
		return nil, fmt.Errorf("mint reviewer key: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"key_id":     key.ID,
		"agent_name": key.AgentName,
		"expires_at": key.ExpiresAt,
	})
	if _, err := ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: in.StoryID,
		Kind:    ledger.KindReviewerKeyMinted,
		Actor:   in.UserID,
		Body:    fmt.Sprintf("minted reviewer key %s", key.ID),
		Payload: payload,
	}, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("mint reviewer key: ledger append: %w", err)
	}

	return &MintReviewerKeyResponse{
		APIKey:    rawKey,
		KeyID:     key.ID,
		StoryID:   in.StoryID,
		ProjectID: key.ProjectID,
		AgentName: key.AgentName,
		ExpiresAt: key.ExpiresAt,
		CreatedAt: key.CreatedAt,
	}, nil
}

// RevokeReviewerKeyForStory revokes the key by id and appends a
// `reviewer_key_revoked` row to the story's ledger. Pairs with
// MintReviewerKeyForStory so the mint/revoke arc is always visible
// in the ledger view.
func RevokeReviewerKeyForStory(ctx context.Context, storyID, keyID, actorUserID string) error {
	if authStore == nil {
		return fmt.Errorf("revoke reviewer key: auth store not configured")
	}
	if ledgerStore == nil {
		return fmt.Errorf("revoke reviewer key: ledger store not configured")
	}
	if strings.TrimSpace(storyID) == "" {
		return fmt.Errorf("revoke reviewer key: story_id required")
	}
	if strings.TrimSpace(keyID) == "" {
		return fmt.Errorf("revoke reviewer key: key_id required")
	}

	if err := authStore.RevokeKey(ctx, keyID); err != nil {
		return fmt.Errorf("revoke reviewer key: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{"key_id": keyID})
	if _, err := ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: storyID,
		Kind:    ledger.KindReviewerKeyRevoked,
		Actor:   actorUserID,
		Body:    fmt.Sprintf("revoked reviewer key %s", keyID),
		Payload: payload,
	}, time.Now().UTC()); err != nil {
		return fmt.Errorf("revoke reviewer key: ledger append: %w", err)
	}
	return nil
}
