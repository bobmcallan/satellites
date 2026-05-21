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
	ProjectID string `json:"project_id"`
}

type StoryListResponse struct {
	Stories []story.Story `json:"stories"`
}

type StoryGetRequest struct {
	ID string `json:"id"`
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
	ss, err := storyStore.ListByProject(ctx, req.ProjectID)
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
	return json.Marshal(s)
}
