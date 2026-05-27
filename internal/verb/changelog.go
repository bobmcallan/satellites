// Changelog verbs — release-note entries surfaced on /changelog.
//
// Ported from V3's internal/models/changelog.go onto the substrate's
// Postgres store (migration 0020). `created_by` is server-derived
// from the auth.FromContext caller and never trusted from the request
// body; the request shape simply omits it.

package verb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/changelog"
)

var changelogStore *changelog.Store

// SetChangelogStore wires the changelog store at server boot.
// CLI-local invocations (no store wired) get an explicit
// not-configured error from the verb, matching the apikey pattern.
func SetChangelogStore(s *changelog.Store) { changelogStore = s }

// ChangelogAddRequest accepts the changelog_add JSON shape. Note
// `created_by` is absent by design — the verb derives it from the
// authed identity.
type ChangelogAddRequest struct {
	Service       string     `json:"service"`
	VersionFrom   string     `json:"version_from,omitempty"`
	VersionTo     string     `json:"version_to"`
	Content       string     `json:"content"`
	EffectiveDate *time.Time `json:"effective_date,omitempty"`
}

// ChangelogListRequest is the input to changelog_list.
type ChangelogListRequest struct {
	Service string `json:"service,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// ChangelogListResponse mirrors document_list's shape: a slice of
// entries + an opaque next_cursor that is empty on the last page.
type ChangelogListResponse struct {
	Items      []changelog.Entry `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// ChangelogUpdateRequest is the input to changelog_update. Pointer
// fields are "set when present"; a present-but-empty string clears
// the column for fields the store allows clearing. EffectiveDate uses
// a double pointer so the operator can distinguish "leave alone"
// (omit), "clear" (`null`), and "set" (`"2026-…"`).
type ChangelogUpdateRequest struct {
	ID            string      `json:"id"`
	Service       *string     `json:"service,omitempty"`
	VersionFrom   *string     `json:"version_from,omitempty"`
	VersionTo     *string     `json:"version_to,omitempty"`
	Content       *string     `json:"content,omitempty"`
	EffectiveDate **time.Time `json:"effective_date,omitempty"`
}

// ChangelogDeleteRequest is the input to changelog_delete.
type ChangelogDeleteRequest struct {
	ID string `json:"id"`
}

func init() {
	Register(&Verb{
		Name:        "changelog_add",
		Description: "Append a release-note entry for a service. created_by is derived from the authed identity and cannot be supplied by the client.",
		Invoke:      invokeChangelogAdd,
	})
	Register(&Verb{
		Name:        "changelog_list",
		Description: "List changelog entries, optionally filtered by service. Cursor-paginated with next_cursor matching document_list semantics.",
		Invoke:      invokeChangelogList,
	})
	Register(&Verb{
		Name:        "changelog_update",
		Description: "Patch a changelog entry by id. Pointer fields are 'set when present'.",
		Invoke:      invokeChangelogUpdate,
	})
	Register(&Verb{
		Name:        "changelog_delete",
		Description: "Hard-delete a changelog entry by id.",
		Invoke:      invokeChangelogDelete,
	})
}

func invokeChangelogAdd(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if changelogStore == nil {
		return nil, fmt.Errorf("changelog_add: changelog store not configured")
	}
	caller := auth.FromContext(ctx)
	if caller == nil {
		return nil, fmt.Errorf("changelog_add: unauthenticated — bearer credential required")
	}
	var req ChangelogAddRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("changelog_add: bad request: %w", err)
		}
	}
	in := changelog.CreateInput{
		Service:       strings.TrimSpace(req.Service),
		VersionFrom:   strings.TrimSpace(req.VersionFrom),
		VersionTo:     strings.TrimSpace(req.VersionTo),
		Content:       req.Content,
		CreatedBy:     caller.ID,
		EffectiveDate: req.EffectiveDate,
	}
	entry, err := changelogStore.Create(ctx, in, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("changelog_add: %w", err)
	}
	return json.Marshal(entry)
}

func invokeChangelogList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if changelogStore == nil {
		return nil, fmt.Errorf("changelog_list: changelog store not configured")
	}
	var req ChangelogListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("changelog_list: bad request: %w", err)
		}
	}
	res, err := changelogStore.List(ctx, changelog.ListOptions{
		Service: strings.TrimSpace(req.Service),
		Cursor:  req.Cursor,
		Limit:   req.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("changelog_list: %w", err)
	}
	out := ChangelogListResponse{
		Items:      res.Items,
		NextCursor: res.NextCursor,
	}
	if out.Items == nil {
		out.Items = []changelog.Entry{}
	}
	return json.Marshal(out)
}

func invokeChangelogUpdate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if changelogStore == nil {
		return nil, fmt.Errorf("changelog_update: changelog store not configured")
	}
	if auth.FromContext(ctx) == nil {
		return nil, fmt.Errorf("changelog_update: unauthenticated — bearer credential required")
	}
	var req ChangelogUpdateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("changelog_update: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("changelog_update: id required")
	}
	entry, err := changelogStore.Update(ctx, req.ID, changelog.UpdateInput{
		Service:       req.Service,
		VersionFrom:   req.VersionFrom,
		VersionTo:     req.VersionTo,
		Content:       req.Content,
		EffectiveDate: req.EffectiveDate,
	}, time.Now().UTC())
	if err != nil {
		if errors.Is(err, changelog.ErrNotFound) {
			return nil, fmt.Errorf("changelog_update: not_found")
		}
		return nil, fmt.Errorf("changelog_update: %w", err)
	}
	return json.Marshal(entry)
}

func invokeChangelogDelete(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if changelogStore == nil {
		return nil, fmt.Errorf("changelog_delete: changelog store not configured")
	}
	if auth.FromContext(ctx) == nil {
		return nil, fmt.Errorf("changelog_delete: unauthenticated — bearer credential required")
	}
	var req ChangelogDeleteRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("changelog_delete: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("changelog_delete: id required")
	}
	if err := changelogStore.Delete(ctx, req.ID); err != nil {
		if errors.Is(err, changelog.ErrNotFound) {
			return nil, fmt.Errorf("changelog_delete: not_found")
		}
		return nil, fmt.Errorf("changelog_delete: %w", err)
	}
	return json.Marshal(map[string]string{"id": req.ID, "deleted": "true"})
}
