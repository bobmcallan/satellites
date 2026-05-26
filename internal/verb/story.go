// Story verbs — V5's units-of-work surface, bound to projects from
// PR 2. All four CRUDs are CLI-primary; the MCP transport gets them
// for free via the shared verb registry.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/story"
)

var storyStore *story.Store

// SetStoryStore wires the server's story.Store into the verb package.
// Called from cmd/satellites-server on boot.
func SetStoryStore(s *story.Store) { storyStore = s }

type StoryCreateRequest struct {
	ProjectID          string   `json:"project_id"`
	ParentID           string   `json:"parent_id,omitempty"`
	Title              string   `json:"title"`
	Body               string   `json:"body,omitempty"`
	AcceptanceCriteria string   `json:"acceptance_criteria,omitempty"`
	Status             string   `json:"status,omitempty"`
	Priority           string   `json:"priority,omitempty"`
	Category           string   `json:"category,omitempty"`
	Tags               []string `json:"tags,omitempty"`
}

type StoryListRequest struct {
	ProjectID string   `json:"project_id"`
	Tags      []string `json:"tags,omitempty"`
}

type StoryListResponse struct {
	Stories []story.Story `json:"stories"`
}

type StoryGetRequest struct {
	ID string `json:"id"`
}

type StoryDeleteRequest struct {
	ID string `json:"id"`
}

type StoryDeleteResponse struct {
	Story story.Story `json:"story"`
}

type StoryUpdateRequest struct {
	ID                 string    `json:"id"`
	ParentID           *string   `json:"parent_id,omitempty"`
	Title              *string   `json:"title,omitempty"`
	Body               *string   `json:"body,omitempty"`
	AcceptanceCriteria *string   `json:"acceptance_criteria,omitempty"`
	Status             *string   `json:"status,omitempty"`
	Priority           *string   `json:"priority,omitempty"`
	Category           *string   `json:"category,omitempty"`
	Tags               *[]string `json:"tags,omitempty"`
}

func init() {
	Register(&Verb{
		Name:        "story_create",
		Description: "Create a new story bound to a project.",
		Invoke:      invokeStoryCreate,
	})
	Register(&Verb{
		Name:        "story_list",
		Description: "List stories under a project, ordered by status + priority + recency.",
		Invoke:      invokeStoryList,
	})
	Register(&Verb{
		Name:        "story_get",
		Description: "Fetch a story by id.",
		Invoke:      invokeStoryGet,
	})
	Register(&Verb{
		Name:        "story_update",
		Description: "Patch mutable fields on a story. project_id is immutable here.",
		Invoke:      invokeStoryUpdate,
	})
	Register(&Verb{
		Name:        "story_delete",
		Description: "Hard-delete a story by id. Child parent_id is nulled; ledger entries persist (append-only).",
		Invoke:      invokeStoryDelete,
	})
}

func invokeStoryCreate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if storyStore == nil {
		return nil, fmt.Errorf("story_create: store not configured")
	}
	var req StoryCreateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("story_create: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, fmt.Errorf("story_create: project_id required")
	}
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("story_create: title required")
	}
	s, err := storyStore.Create(ctx, story.CreateInput{
		ProjectID:          req.ProjectID,
		ParentID:           req.ParentID,
		Title:              req.Title,
		Body:               req.Body,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Status:             req.Status,
		Priority:           req.Priority,
		Category:           req.Category,
		Tags:               req.Tags,
	}, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if ledgerStore != nil {
		payload, _ := json.Marshal(s)
		if _, lerr := ledgerStore.Append(ctx, ledger.AppendInput{
			StoryID: s.ID,
			Kind:    ledger.KindStoryCreated,
			Actor:   actorFromContext(ctx),
			Payload: payload,
		}, time.Now().UTC()); lerr != nil {
			return nil, fmt.Errorf("story_create: ledger append: %w", lerr)
		}
		dispatchSummaryRegen(ctx, s.ID)
	}
	dispatchReviewers(ctx, s)
	return json.Marshal(s)
}

func invokeStoryList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if storyStore == nil {
		return nil, fmt.Errorf("story_list: store not configured")
	}
	var req StoryListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("story_list: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, fmt.Errorf("story_list: project_id required")
	}
	ss, err := storyStore.ListByProject(ctx, req.ProjectID, req.Tags)
	if err != nil {
		return nil, err
	}
	if ss == nil {
		ss = []story.Story{}
	}
	return json.Marshal(StoryListResponse{Stories: ss})
}

func invokeStoryGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if storyStore == nil {
		return nil, fmt.Errorf("story_get: store not configured")
	}
	var req StoryGetRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("story_get: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("story_get: id required")
	}
	s, err := storyStore.GetByID(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

func invokeStoryUpdate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if storyStore == nil {
		return nil, fmt.Errorf("story_update: store not configured")
	}
	var req StoryUpdateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("story_update: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("story_update: id required")
	}
	var before story.Story
	if ledgerStore != nil {
		var err error
		before, err = storyStore.GetByID(ctx, req.ID)
		if err != nil {
			return nil, err
		}
	}
	s, err := storyStore.Update(ctx, req.ID, story.UpdateInput{
		ParentID:           req.ParentID,
		Title:              req.Title,
		Body:               req.Body,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Status:             req.Status,
		Priority:           req.Priority,
		Category:           req.Category,
		Tags:               req.Tags,
	}, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if ledgerStore != nil {
		diff := computeStoryDiff(before, s)
		if len(diff) > 0 {
			payload, _ := json.Marshal(diff)
			if _, lerr := ledgerStore.Append(ctx, ledger.AppendInput{
				StoryID: s.ID,
				Kind:    ledger.KindStoryUpdated,
				Actor:   actorFromContext(ctx),
				Payload: payload,
			}, time.Now().UTC()); lerr != nil {
				return nil, fmt.Errorf("story_update: ledger append: %w", lerr)
			}
			dispatchSummaryRegen(ctx, s.ID)
		}
	}
	dispatchReviewers(ctx, s)
	return json.Marshal(s)
}

func invokeStoryDelete(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if storyStore == nil {
		return nil, fmt.Errorf("story_delete: store not configured")
	}
	var req StoryDeleteRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("story_delete: bad request: %w", err)
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("story_delete: id required")
	}
	s, err := storyStore.GetByID(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if err := storyStore.Delete(ctx, req.ID); err != nil {
		return nil, err
	}
	return json.Marshal(StoryDeleteResponse{Story: s})
}

// computeStoryDiff returns a {field: {before, after}} map for each
// field whose value changed between before and after. Returns an
// empty map when nothing changed — the caller skips the ledger
// append in that case so no-op updates don't pollute the log.
func computeStoryDiff(before, after story.Story) map[string]map[string]any {
	diff := map[string]map[string]any{}
	if before.ParentID != after.ParentID {
		diff["parent_id"] = map[string]any{"before": before.ParentID, "after": after.ParentID}
	}
	if before.Title != after.Title {
		diff["title"] = map[string]any{"before": before.Title, "after": after.Title}
	}
	if before.Body != after.Body {
		diff["body"] = map[string]any{"before": before.Body, "after": after.Body}
	}
	if before.AcceptanceCriteria != after.AcceptanceCriteria {
		diff["acceptance_criteria"] = map[string]any{"before": before.AcceptanceCriteria, "after": after.AcceptanceCriteria}
	}
	if before.Status != after.Status {
		diff["status"] = map[string]any{"before": before.Status, "after": after.Status}
	}
	if before.Priority != after.Priority {
		diff["priority"] = map[string]any{"before": before.Priority, "after": after.Priority}
	}
	if before.Category != after.Category {
		diff["category"] = map[string]any{"before": before.Category, "after": after.Category}
	}
	if !stringSlicesEqual(before.Tags, after.Tags) {
		diff["tags"] = map[string]any{"before": before.Tags, "after": after.Tags}
	}
	return diff
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
