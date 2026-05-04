package portal

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// storyWalkData is the SSR view-model for the contract walk page
// (sty_557df61e). Cards group by ContractName; each group's lap count
// is the headline header so loop-iteration is unmistakable.
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

// walkGroup bundles every CI of one ContractName for the iteration-grouped
// header rendering. Cards is in workflow order — the first card is the
// first lap, the last is the most recent.
type walkGroup struct {
	ContractName string
	Iterations   int
	Cards        []walkCard
}

// walkCard is one CI row in the timeline. Href targets the existing
// ledger detail view filtered by contract_id so click-through pays out
// the "evidence is the trust leverage" property.
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
	if p.stories == nil || p.contracts == nil {
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

	cis, err := p.contracts.List(r.Context(), storyID, memberships)
	if err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	groups, currentCIID := buildStoryWalkGroups(r.Context(), cis, st.ProjectID, p.documents, p.tasks, p.ledger, memberships)

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
		CurrentCIID:     currentCIID,
	}
	if proj.ID != "" {
		data.BackHref = fmt.Sprintf("/projects/%s/stories/%s", proj.ID, st.ID)
	}
	if err := p.tmpl.ExecuteTemplate(w, "story_walk.html", data); err != nil {
		p.logger.Error().Str("template", "story_walk.html").Str("error", err.Error()).Msg("template render failed")
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// buildStoryWalkGroups projects the CI list into iteration-grouped
// cards. Returns the group slice in workflow order plus the
// current_ci_id (first non-terminal CI).
func buildStoryWalkGroups(
	ctx context.Context,
	cis []contract.ContractInstance,
	projectID string,
	docs document.Store,
	tasks task.Store,
	led ledger.Store,
	memberships []string,
) ([]walkGroup, string) {
	if len(cis) == 0 {
		return nil, ""
	}

	ledgerByCI := map[string]int{}
	if led != nil && projectID != "" {
		rows, err := led.List(ctx, projectID, ledger.ListOptions{Limit: ledger.MaxListLimit}, memberships)
		if err == nil {
			for _, row := range rows {
				if row.ContractID == nil {
					continue
				}
				ledgerByCI[*row.ContractID]++
			}
		}
	}

	contractDocCache := map[string]document.Document{}
	resolveDoc := func(id string) document.Document {
		if id == "" || docs == nil {
			return document.Document{}
		}
		if cached, ok := contractDocCache[id]; ok {
			return cached
		}
		doc, err := docs.GetByID(ctx, id, nil)
		if err != nil {
			return document.Document{}
		}
		contractDocCache[id] = doc
		return doc
	}

	currentCIID := ""
	groupIndex := map[string]int{}
	groups := make([]walkGroup, 0)

	for _, ci := range cis {
		iter := iterationFromPeers(ci, cis)
		card := walkCard{
			ID:             ci.ID,
			ContractName:   ci.ContractName,
			Sequence:       ci.Sequence,
			Iteration:      iter,
			Status:         ci.Status,
			LedgerRowCount: ledgerByCI[ci.ID],
			LedgerHref:     fmt.Sprintf("/projects/%s/ledger?contract_id=%s", projectID, ci.ID),
		}
		if !ci.ClaimedAt.IsZero() {
			card.ClaimedAt = ci.ClaimedAt.UTC().Format("2006-01-02 15:04:05")
		}
		switch ci.Status {
		case contract.StatusPassed, contract.StatusFailed, contract.StatusSkipped:
			card.ClosedAt = ci.UpdatedAt.UTC().Format("2006-01-02 15:04:05")
			card.Outcome = ci.Status
		}
		_ = resolveDoc(ci.ContractID)
		card.TaskSummary = walkTaskSummaryFor(ctx, tasks, ci.ID, memberships)
		card.ClaimedByUser = walkLookupActionClaimUser(ctx, led, projectID, ci.ID, memberships)
		if currentCIID == "" {
			switch ci.Status {
			case contract.StatusPassed, contract.StatusFailed, contract.StatusSkipped:
				// terminal — keep scanning
			default:
				currentCIID = ci.ID
				card.IsCurrent = true
			}
		}
		idx, exists := groupIndex[ci.ContractName]
		if !exists {
			groupIndex[ci.ContractName] = len(groups)
			groups = append(groups, walkGroup{
				ContractName: ci.ContractName,
				Iterations:   1,
				Cards:        []walkCard{card},
			})
			continue
		}
		groups[idx].Iterations++
		groups[idx].Cards = append(groups[idx].Cards, card)
	}
	// Stable order: groups already follow workflow sequence because
	// cis is sequence-ordered. Cards within group also follow the same
	// scan order.
	return groups, currentCIID
}

// iterationFromPeers is intentionally re-defined here even though
// mcpserver carries the same helper — keeping the two packages
// independent. Both implementations are pure functions over the same
// slice and stay aligned by the shared invariant: same ContractName +
// CreatedAt <= ci's gives the lap number.
//
// Re-defined locally to avoid a cross-package import (portal already
// imports contract; mcpserver doesn't depend on portal).
func iterationFromPeers(ci contract.ContractInstance, peers []contract.ContractInstance) int {
	n := 0
	for _, p := range peers {
		if p.ContractName != ci.ContractName {
			continue
		}
		if p.CreatedAt.After(ci.CreatedAt) {
			continue
		}
		n++
	}
	if n == 0 {
		return 1
	}
	return n
}

func walkTaskSummaryFor(ctx context.Context, tasks task.Store, ciID string, memberships []string) walkTaskCounts {
	out := walkTaskCounts{}
	if tasks == nil || ciID == "" {
		return out
	}
	rows, err := tasks.List(ctx, task.ListOptions{ContractInstanceID: ciID, Limit: 500}, memberships)
	if err != nil {
		return out
	}
	for _, t := range rows {
		switch t.Status {
		case task.StatusEnqueued:
			out.Enqueued++
		case task.StatusClaimed, task.StatusInFlight:
			out.InFlight++
		case task.StatusClosed:
			switch t.Outcome {
			case task.OutcomeSuccess:
				out.ClosedSuccess++
			case task.OutcomeFailure:
				out.ClosedFailure++
			case task.OutcomeTimeout:
				out.ClosedTimeout++
			}
		}
	}
	return out
}

func walkLookupActionClaimUser(ctx context.Context, led ledger.Store, projectID, ciID string, memberships []string) string {
	if led == nil || projectID == "" || ciID == "" {
		return ""
	}
	rows, err := led.List(ctx, projectID, ledger.ListOptions{
		Type:       ledger.TypeActionClaim,
		ContractID: ciID,
		Limit:      32,
	}, memberships)
	if err != nil || len(rows) == 0 {
		return ""
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows[0].CreatedBy
}
