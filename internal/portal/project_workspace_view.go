// Project workspace composite for story_6a0aca5a. The composite renders
// the panels on project_detail.html as previews; the dedicated
// per-section pages own full search and filtering. Document rows mirror
// the documents_view.go shape so the cards stay consistent.
//
// sty_70c0f7a3 extended this composite with repo, contracts, and recent
// ledger teasers so the consolidated project page can render every
// panel from a single handler call.
package portal

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/repo"
	"github.com/bobmcallan/satellites/internal/story"
)

const (
	projectWorkspaceDefaultLimit = 25
	projectWorkspaceMaxLimit     = 200
	projectWorkspaceListLimit    = 200
)

// projectWorkspaceComposite is the view-model for the project_detail page.
// Each field maps to a panel in the panel registry.
type projectWorkspaceComposite struct {
	Stories        []storyCard
	Documents      []documentCard
	Contracts      []documentCard
	Repo           repoCard
	RepoEmpty      bool
	LedgerRecent   []ledgerRowView
	LedgerByStory  map[string][]ledgerRowView
	Filters        projectWorkspaceFilters
	StoryTotal     int
	DocTotal       int
	ContractsTotal int
	LedgerTotal    int
}

// storyCard is the per-row view-model for the Stories section. The
// expand-row on the V3-style story panel reads Description and
// AcceptanceCriteria.
type storyCard struct {
	ID                 string
	ProjectID          string
	Title              string
	Status             string
	Priority           string
	Tags               []string
	UpdatedAt          string
	Description        string
	AcceptanceCriteria string
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

// buildProjectWorkspaceComposite assembles the composite from the story,
// document, repo, and ledger stores. Any store may be nil — the
// corresponding section is empty in that case so callers degrade
// gracefully when running without a backing store. Documents are loaded
// twice (project scope + system scope) and merged so global content
// (principles, reviewer notes) shows alongside the project's own.
func buildProjectWorkspaceComposite(ctx context.Context, stories story.Store, docs document.Store, repos repo.Store, led ledger.Store, projectID string, f projectWorkspaceFilters, memberships []string, isAdmin bool) projectWorkspaceComposite {
	if f.Limit <= 0 {
		f.Limit = projectWorkspaceDefaultLimit
	}
	out := projectWorkspaceComposite{Filters: f}

	out.Stories = collectStoryCards(ctx, stories, projectID, f, memberships)
	out.StoryTotal = len(out.Stories)

	out.Documents = collectDocumentCards(ctx, docs, projectID, f, memberships)
	out.DocTotal = len(out.Documents)

	if docs != nil {
		out.Contracts = collectConfigCards(ctx, docs, projectID, document.TypeContract, memberships)
		out.ContractsTotal = len(out.Contracts)
		if len(out.Contracts) > f.Limit {
			out.Contracts = out.Contracts[:f.Limit]
		}
	}

	repoComp := buildRepoComposite(ctx, repos, projectID, memberships, isAdmin)
	out.Repo = repoComp.Repo
	out.RepoEmpty = repoComp.Empty

	out.LedgerRecent = collectRecentLedger(ctx, led, projectID, f, memberships)
	out.LedgerTotal = len(out.LedgerRecent)
	out.LedgerByStory = groupLedgerByStory(out.LedgerRecent, ledgerPerStoryCap)

	return out
}

// ledgerPerStoryCap caps the per-story ledger preview shown in the
// story panel's expand-row. Sourced from the composite's LedgerRecent,
// so no extra queries.
const ledgerPerStoryCap = 3

// groupLedgerByStory groups recent ledger rows by their StoryID, keeping
// the original (UpdatedAt-desc) order from the source list. Each story's
// list is capped at perStory.
func groupLedgerByStory(rows []ledgerRowView, perStory int) map[string][]ledgerRowView {
	out := make(map[string][]ledgerRowView)
	for _, r := range rows {
		if r.StoryID == "" {
			continue
		}
		if len(out[r.StoryID]) >= perStory {
			continue
		}
		out[r.StoryID] = append(out[r.StoryID], r)
	}
	return out
}

// collectRecentLedger reads the most recent ledger rows for the project,
// capped at f.Limit. Returns an empty slice when the store is nil or
// errors so the page still renders.
func collectRecentLedger(ctx context.Context, led ledger.Store, projectID string, f projectWorkspaceFilters, memberships []string) []ledgerRowView {
	if led == nil || projectID == "" {
		return []ledgerRowView{}
	}
	rows, err := led.List(ctx, projectID, ledger.ListOptions{Limit: f.Limit}, memberships)
	if err != nil {
		return []ledgerRowView{}
	}
	out := make([]ledgerRowView, 0, len(rows))
	for _, r := range rows {
		out = append(out, ledgerRowViewFor(r))
	}
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
		ID:                 s.ID,
		ProjectID:          s.ProjectID,
		Title:              s.Title,
		Status:             s.Status,
		Priority:           s.Priority,
		Tags:               s.Tags,
		UpdatedAt:          s.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		Description:        s.Description,
		AcceptanceCriteria: s.AcceptanceCriteria,
	}
}
