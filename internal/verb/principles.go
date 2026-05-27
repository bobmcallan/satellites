// Principles — substrate ride-along on read verbs.
//
// A principle is any type='document' tagged `principles:<scope>`. Read
// verbs whose response crosses a scope boundary (document_get on a
// story, project_get, project_match, project_list with workspace
// filter) attach the matching set under a `principles` sidecar so the
// agent sees them on every call rather than once at startup.
//
// Write verbs do NOT carry the sidecar; principles are CRUDed through
// the existing document_* verbs like any other document.

package verb

import (
	"context"
	"fmt"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
)

// PrincipleScope encodes the substrate scope a principle applies to —
// stored on the document as the tag `principles:<scope>`.
type PrincipleScope string

const (
	PrincipleScopeGlobal    PrincipleScope = "global"
	PrincipleScopeWorkspace PrincipleScope = "workspace"
	PrincipleScopeProject   PrincipleScope = "project"
	PrincipleScopeStory     PrincipleScope = "story"
)

// PrincipleTag returns the tag string that marks a document as a
// principle at the given scope.
func PrincipleTag(s PrincipleScope) string { return "principles:" + string(s) }

// Principle is one entry in the sidecar — the document's identity plus
// the latest active body. Body carries the full markdown; agents read
// it directly without a second fetch.
type Principle struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Scope     PrincipleScope `json:"scope"`
	Body      string         `json:"body"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// PrincipleScopeRequest names a scope the caller wants principles for,
// plus the workspace/project ids identifying which substrate scope
// applies. Global requests ignore the ids; workspace requires
// WorkspaceID; project and story require both ids.
type PrincipleScopeRequest struct {
	Scope       PrincipleScope
	WorkspaceID string
	ProjectID   string
}

// LoadPrinciples returns active principle documents matching any of
// the requested scopes, concatenated in input order. Failures on a
// single scope are non-fatal — sidecar lookups never break the primary
// read verb. nil documentStore (test setup, CLI-local boot) returns
// nil quietly.
func LoadPrinciples(ctx context.Context, reqs ...PrincipleScopeRequest) []Principle {
	if documentStore == nil || len(reqs) == 0 {
		return nil
	}
	var out []Principle
	for _, r := range reqs {
		batch, err := loadPrinciplesOne(ctx, r)
		if err != nil {
			continue
		}
		out = append(out, batch...)
	}
	return out
}

func loadPrinciplesOne(ctx context.Context, r PrincipleScopeRequest) ([]Principle, error) {
	filter := document.ListFilter{
		Type:   document.TypeDocument,
		Tags:   []string{PrincipleTag(r.Scope)},
		Status: string(document.StatusActive),
	}
	switch r.Scope {
	case PrincipleScopeGlobal:
		filter.Scope = document.ScopeSystem
	case PrincipleScopeWorkspace:
		if r.WorkspaceID == "" {
			return nil, nil
		}
		filter.Scope = document.ScopeWorkspace
		filter.WorkspaceID = r.WorkspaceID
	case PrincipleScopeProject, PrincipleScopeStory:
		if r.WorkspaceID == "" || r.ProjectID == "" {
			return nil, nil
		}
		filter.Scope = document.ScopeProject
		filter.WorkspaceID = r.WorkspaceID
		filter.ProjectID = r.ProjectID
	default:
		return nil, fmt.Errorf("principles: unknown scope %q", r.Scope)
	}
	res, err := documentStore.List(ctx, filter, document.ListOptions{Limit: 200})
	if err != nil {
		return nil, err
	}
	out := make([]Principle, 0, len(res.Items))
	for _, d := range res.Items {
		// A soft-deleted document keeps its tag and its previous active
		// body — the document_versions chain just gains a tombstone at
		// the top. Check the LATEST version's status to skip those.
		latest, err := documentStore.LatestVersionStatus(ctx, d.ID)
		if err != nil || latest != document.StatusActive {
			continue
		}
		_, body, err := documentStore.GetByIDWithLatestBody(ctx, d.ID)
		if err != nil || body == "" {
			continue
		}
		out = append(out, Principle{
			ID:        d.ID,
			Name:      d.Name,
			Scope:     r.Scope,
			Body:      body,
			UpdatedAt: d.UpdatedAt,
		})
	}
	return out, nil
}
