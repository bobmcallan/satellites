// Project workspace composite for story_6a0aca5a. The composite renders
// two sections (Stories, Documents) on project_detail.html as previews;
// the dedicated /projects/<id>/stories page owns search and filtering
// (story_59b11d8c). Document rows mirror the documents_view.go shape so
// the cards stay consistent.
package portal

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/story"
)

const (
	projectWorkspaceDefaultLimit = 25
	projectWorkspaceMaxLimit     = 200
	projectWorkspaceListLimit    = 200
)

// projectWorkspaceComposite is the view-model for the project_detail page.
// Stories and Documents render in two distinct sections.
type projectWorkspaceComposite struct {
	Stories    []storyCard
	Documents  []documentCard
	Filters    projectWorkspaceFilters
	StoryTotal int
	DocTotal   int
}

// storyCard is the per-row view-model for the Stories section.
type storyCard struct {
	ID        string
	ProjectID string
	Title     string
	Status    string
	Priority  string
	Tags      []string
	UpdatedAt string
}

// projectWorkspaceFilters carries the per-section row cap.
type projectWorkspaceFilters struct {
	Limit int
}

// parseProjectWorkspaceFilters reads `?limit=` from the request, clamping
// to [1, projectWorkspaceMaxLimit].
func parseProjectWorkspaceFilters(r *http.Request) projectWorkspaceFilters {
	q := r.URL.Query()
	f := projectWorkspaceFilters{Limit: projectWorkspaceDefaultLimit}
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > projectWorkspaceMaxLimit {
				n = projectWorkspaceMaxLimit
			}
			f.Limit = n
		}
	}
	return f
}

// buildProjectWorkspaceComposite assembles the composite from the story
// and document stores. Either store may be nil — the corresponding section
// is empty in that case so callers degrade gracefully when running without
// a backing store. Documents are loaded twice (project scope + system
// scope) and merged so global content (principles, reviewer notes) shows
// alongside the project's own.
func buildProjectWorkspaceComposite(ctx context.Context, stories story.Store, docs document.Store, projectID string, f projectWorkspaceFilters, memberships []string) projectWorkspaceComposite {
	if f.Limit <= 0 {
		f.Limit = projectWorkspaceDefaultLimit
	}
	out := projectWorkspaceComposite{Filters: f}

	out.Stories = collectStoryCards(ctx, stories, projectID, f, memberships)
	out.StoryTotal = len(out.Stories)

	out.Documents = collectDocumentCards(ctx, docs, projectID, f, memberships)
	out.DocTotal = len(out.Documents)

	return out
}

// collectStoryCards lists project-scoped stories, sorts by UpdatedAt desc,
// and caps at f.Limit. Returns an empty slice when the store is nil or
// errors — the page still renders. Filtering lives on the dedicated
// /projects/<id>/stories page (story_59b11d8c).
func collectStoryCards(ctx context.Context, stories story.Store, projectID string, f projectWorkspaceFilters, memberships []string) []storyCard {
	if stories == nil || projectID == "" {
		return []storyCard{}
	}
	rows, err := stories.List(ctx, projectID, story.ListOptions{Limit: projectWorkspaceListLimit}, memberships)
	if err != nil {
		return []storyCard{}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].UpdatedAt.After(rows[j].UpdatedAt) })
	if len(rows) > f.Limit {
		rows = rows[:f.Limit]
	}
	out := make([]storyCard, 0, len(rows))
	for _, s := range rows {
		out = append(out, storyCardFor(s))
	}
	return out
}

// collectDocumentCards loads documents in project scope and system scope,
// drops contract + skill types (those live on the Configuration tab),
// dedupes by id, sorts by UpdatedAt desc, and caps at f.Limit.
func collectDocumentCards(ctx context.Context, docs document.Store, projectID string, f projectWorkspaceFilters, memberships []string) []documentCard {
	if docs == nil {
		return []documentCard{}
	}
	rows := loadProjectAndSystemDocs(ctx, docs, projectID, memberships)
	rows = excludeConfigDocs(rows)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].UpdatedAt.After(rows[j].UpdatedAt) })
	if len(rows) > f.Limit {
		rows = rows[:f.Limit]
	}
	out := make([]documentCard, 0, len(rows))
	for _, d := range rows {
		out = append(out, documentCardFor(d))
	}
	return out
}

// loadProjectAndSystemDocs runs two List calls (project + system scope)
// and merges the results, deduping by id. A nil projectID skips the
// project scope.
func loadProjectAndSystemDocs(ctx context.Context, docs document.Store, projectID string, memberships []string) []document.Document {
	seen := make(map[string]struct{})
	out := make([]document.Document, 0)
	if projectID != "" {
		projectRows, err := docs.List(ctx, document.ListOptions{ProjectID: projectID, Limit: projectWorkspaceListLimit}, memberships)
		if err == nil {
			for _, d := range projectRows {
				if _, dup := seen[d.ID]; dup {
					continue
				}
				seen[d.ID] = struct{}{}
				out = append(out, d)
			}
		}
	}
	systemRows, err := docs.List(ctx, document.ListOptions{Scope: document.ScopeSystem, Limit: projectWorkspaceListLimit}, memberships)
	if err == nil {
		for _, d := range systemRows {
			if _, dup := seen[d.ID]; dup {
				continue
			}
			seen[d.ID] = struct{}{}
			out = append(out, d)
		}
	}
	return out
}

// excludeConfigDocs drops contract and skill types — those are lifecycle
// configuration and live on a separate Configuration tab (story_433d0661).
func excludeConfigDocs(rows []document.Document) []document.Document {
	out := make([]document.Document, 0, len(rows))
	for _, d := range rows {
		if d.Type == document.TypeContract || d.Type == document.TypeSkill {
			continue
		}
		out = append(out, d)
	}
	return out
}

// storyCardFor projects a story.Story into the card view-model.
func storyCardFor(s story.Story) storyCard {
	return storyCard{
		ID:        s.ID,
		ProjectID: s.ProjectID,
		Title:     s.Title,
		Status:    s.Status,
		Priority:  s.Priority,
		Tags:      s.Tags,
		UpdatedAt: s.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}
