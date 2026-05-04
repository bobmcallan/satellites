package portal

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// storyWalkData is the SSR view-model for the contract walk page
// (sty_557df61e). sty_c6d76a5b checkpoint 14 retired contract_instance
// rows; the walk now projects from the task chain.
type storyWalkData struct {
	Title           string
	Version         string
	Commit          string
	User            auth.User
	Project         projectRow
	Story           storyRow
	Workspaces      []wsChip
	ActiveWorkspace wsChip
	DevMode         bool
	GlobalAdminChip bool
	IsGlobalAdmin   bool
	ThemeMode       string
	ThemePickerNext string
	WSConfig        WSConfig
	Groups          []walkGroup
	CurrentCIID     string
	BackHref        string
}

// walkGroup bundles every task with one Action for iteration-grouped
// header rendering. Cards is in workflow order (created_at ascending).
type walkGroup struct {
	ContractName string
	Iterations   int
	Cards        []walkCard
}

// walkCard is one task row in the timeline. ID is the task id (the
// template binds `data-ci-id` for back-compat with the prior CI-based
// view). LedgerHref targets the per-task ledger filter so click-through
// pays out the "evidence is the trust leverage" property.
type walkCard struct {
	ID             string
	ContractName   string
	Sequence       int
	Iteration      int
	Status         string
	ClaimedByUser  string
	ClaimedAt      string
	ClosedAt       string
	Outcome        string
	LedgerRowCount int
	TaskSummary    walkTaskCounts
	LedgerHref     string
	IsCurrent      bool
}

// walkTaskCounts is a per-card review-side summary so the work card
// surfaces whether its sibling review has fired and how it landed.
type walkTaskCounts struct {
	Enqueued      int
	InFlight      int
	ClosedSuccess int
	ClosedFailure int
	ClosedTimeout int
}

// handleStoryWalk renders the contract walk page for one story
// (sty_557df61e). Reads via the same in-process stores as the
// task_walk MCP verb; cross-workspace returns 404 to avoid leaking
// story existence.
func (p *Portal) handleStoryWalk(w http.ResponseWriter, r *http.Request) {
	user, ok := p.resolveUser(r)
	if !ok {
		p.redirectToLogin(w, r)
		return
	}
	if p.stories == nil || p.tasks == nil {
		http.NotFound(w, r)
		return
	}
	storyID := r.PathValue("story_id")
	active, chips, memberships := p.activeWorkspace(r, user)
	st, err := p.stories.GetByID(r.Context(), storyID, memberships)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	proj := projectRow{}
	if p.projects != nil && st.ProjectID != "" {
		if pr, perr := p.projects.GetByID(r.Context(), st.ProjectID, memberships); perr == nil {
			proj = viewRow(pr)
		}
	}

	tasks, err := p.tasks.List(r.Context(), task.ListOptions{StoryID: storyID, Limit: 500}, memberships)
	if err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	groups, currentTaskID := buildStoryWalkGroups(r.Context(), tasks, st.ProjectID, p.ledger, memberships)

	data := storyWalkData{
		Title:           buildPageTitle(active, st.Title, "walk"),
		Version:         config.Version,
		Commit:          config.GitCommit,
		User:            user,
		Project:         proj,
		Story:           viewStoryRow(st),
		Workspaces:      chips,
		ActiveWorkspace: active,
		DevMode:         p.cfg.Env != "prod" && p.cfg.DevMode,
		GlobalAdminChip: p.globalAdminChip(user, active, memberships),
		IsGlobalAdmin:   p.isGlobalAdmin(user),
		ThemeMode:       themeFromRequest(r),
		ThemePickerNext: r.URL.RequestURI(),
		WSConfig:        buildWSConfig(active, r),
		Groups:          groups,
		CurrentCIID:     currentTaskID,
	}
	if proj.ID != "" {
		data.BackHref = fmt.Sprintf("/projects/%s/stories/%s", proj.ID, st.ID)
	}
	if err := p.tmpl.ExecuteTemplate(w, "story_walk.html", data); err != nil {
		p.logger.Error().Str("template", "story_walk.html").Str("error", err.Error()).Msg("template render failed")
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// buildStoryWalkGroups projects the task list into action-grouped
// cards. Returns the group slice in workflow order plus the current
// task id (first non-terminal task).
func buildStoryWalkGroups(
	ctx context.Context,
	tasks []task.Task,
	projectID string,
	led ledger.Store,
	memberships []string,
) ([]walkGroup, string) {
	if len(tasks) == 0 {
		return nil, ""
	}

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
	})

	// ledger row counts per task — uses the `task_id:<id>` tag the
	// reviewer service + agent stamp on every task-scoped row.
	ledgerByTask := map[string]int{}
	if led != nil && projectID != "" {
		rows, err := led.List(ctx, projectID, ledger.ListOptions{Limit: ledger.MaxListLimit}, memberships)
		if err == nil {
			for _, row := range rows {
				for _, tag := range row.Tags {
					if tid, ok := taskIDFromTag(tag); ok {
						ledgerByTask[tid]++
					}
				}
			}
		}
	}

	// Aggregate work-task siblings by ParentTaskID so each work card
	// can show its review-task summary.
	reviewByParent := map[string]walkTaskCounts{}
	for _, t := range tasks {
		if t.Kind != task.KindReview || t.ParentTaskID == "" {
			continue
		}
		c := reviewByParent[t.ParentTaskID]
		switch t.Status {
		case task.StatusEnqueued, task.StatusPlanned, task.StatusPublished:
			c.Enqueued++
		case task.StatusClaimed, task.StatusInFlight:
			c.InFlight++
		case task.StatusClosed:
			switch t.Outcome {
			case task.OutcomeSuccess:
				c.ClosedSuccess++
			case task.OutcomeFailure:
				c.ClosedFailure++
			case task.OutcomeTimeout:
				c.ClosedTimeout++
			}
		}
		reviewByParent[t.ParentTaskID] = c
	}

	currentTaskID := ""
	groupIndex := map[string]int{}
	groups := make([]walkGroup, 0)

	for _, t := range tasks {
		// Show work tasks as the principal cards; review tasks roll up
		// into the work card's TaskSummary via reviewByParent.
		if t.Kind == task.KindReview {
			continue
		}
		contractName := contractNameFromAction(t.Action)
		card := walkCard{
			ID:             t.ID,
			ContractName:   contractName,
			Iteration:      walkIterationForTask(t, tasks),
			Status:         walkCardStatus(t),
			LedgerRowCount: ledgerByTask[t.ID],
			LedgerHref:     fmt.Sprintf("/projects/%s/ledger?task_id=%s", projectID, t.ID),
			ClaimedByUser:  t.ClaimedBy,
			TaskSummary:    reviewByParent[t.ID],
		}
		if t.ClaimedAt != nil {
			card.ClaimedAt = t.ClaimedAt.UTC().Format("2006-01-02 15:04:05")
		}
		if t.CompletedAt != nil {
			card.ClosedAt = t.CompletedAt.UTC().Format("2006-01-02 15:04:05")
			card.Outcome = t.Outcome
		}
		if currentTaskID == "" && t.Status != task.StatusClosed && t.Status != task.StatusArchived {
			currentTaskID = t.ID
			card.IsCurrent = true
		}

		groupKey := contractName
		if groupKey == "" {
			groupKey = "(no contract)"
		}
		idx, exists := groupIndex[groupKey]
		if !exists {
			groupIndex[groupKey] = len(groups)
			groups = append(groups, walkGroup{
				ContractName: groupKey,
				Iterations:   1,
				Cards:        []walkCard{card},
			})
			continue
		}
		groups[idx].Iterations++
		groups[idx].Cards = append(groups[idx].Cards, card)
	}
	return groups, currentTaskID
}

// walkCardStatus collapses task status + outcome into the visual
// status pill. Closed tasks render their outcome (success/failed/timeout)
// so the timeline reads like the contract walk it replaced.
func walkCardStatus(t task.Task) string {
	if t.Status == task.StatusClosed {
		switch t.Outcome {
		case task.OutcomeSuccess:
			return "passed"
		case task.OutcomeFailure:
			return "failed"
		case task.OutcomeTimeout:
			return "timeout"
		}
	}
	return t.Status
}

// walkIterationForTask returns the 1-based lap of t among kind:work
// tasks with the same Action on the same story (created_at ordered).
func walkIterationForTask(t task.Task, peers []task.Task) int {
	if t.Action == "" {
		return 1
	}
	n := 0
	for _, p := range peers {
		if p.Action != t.Action || p.Kind != t.Kind {
			continue
		}
		if p.CreatedAt.After(t.CreatedAt) {
			continue
		}
		n++
	}
	if n == 0 {
		return 1
	}
	return n
}

// taskIDFromTag pulls the task id out of a `task_id:<id>` tag.
func taskIDFromTag(tag string) (string, bool) {
	const prefix = "task_id:"
	if len(tag) <= len(prefix) || tag[:len(prefix)] != prefix {
		return "", false
	}
	return tag[len(prefix):], true
}

// contractNameFromAction unwraps `contract:<name>` into the bare name.
// Empty when the action isn't a contract action.
func contractNameFromAction(action string) string {
	const prefix = "contract:"
	if len(action) <= len(prefix) || action[:len(prefix)] != prefix {
		return ""
	}
	return action[len(prefix):]
}
