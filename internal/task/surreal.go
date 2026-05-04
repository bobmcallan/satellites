package task

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/surrealdb/surrealdb.go"
	surrealmodels "github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bobmcallan/satellites/internal/hubemit"
)

// SurrealStore is a SurrealDB-backed Store. Atomic Claim uses a single
// UPDATE query with ORDER BY + LIMIT 1 so two workers cannot double-claim
// the same row.
type SurrealStore struct {
	db        *surrealdb.DB
	publisher hubemit.Publisher
}

// SetPublisher installs the hub emit sink for subsequent mutations.
func (s *SurrealStore) SetPublisher(p hubemit.Publisher) { s.publisher = p }

// NewSurrealStore wraps db as a Store and defines the `tasks` table +
// the three indexes the dispatcher + worker heartbeat queries rely on.
func NewSurrealStore(db *surrealdb.DB) *SurrealStore {
	s := &SurrealStore{db: db}
	_, _ = surrealdb.Query[any](context.Background(), db, "DEFINE TABLE IF NOT EXISTS tasks SCHEMALESS", nil)
	_, _ = surrealdb.Query[any](context.Background(), db, "DEFINE INDEX IF NOT EXISTS tasks_dispatch ON TABLE tasks FIELDS workspace_id, status, priority, created_at", nil)
	_, _ = surrealdb.Query[any](context.Background(), db, "DEFINE INDEX IF NOT EXISTS tasks_worker ON TABLE tasks FIELDS workspace_id, claimed_by", nil)
	_, _ = surrealdb.Query[any](context.Background(), db, "DEFINE INDEX IF NOT EXISTS tasks_archival ON TABLE tasks FIELDS workspace_id, completed_at", nil)
	return s
}

const selectCols = "meta::id(id) AS id, workspace_id, project_id, contract_instance_id, kind, iteration, agent_id, prior_task_id, parent_task_id, origin, trigger, payload, status, priority, claimed_by, claimed_at, completed_at, outcome, ledger_root_id, expected_duration, reclaim_count, created_at"

// Enqueue implements Store for SurrealStore.
//
// Accepts t.Status ∈ {planned, published, enqueued (legacy default)}.
// Empty defaults to StatusEnqueued for back-compat. sty_c1200f75.
func (s *SurrealStore) Enqueue(ctx context.Context, t Task, now time.Time) (Task, error) {
	if t.Status == "" {
		t.Status = StatusEnqueued
	}
	if t.Priority == "" {
		t.Priority = PriorityMedium
	}
	if t.Iteration <= 0 {
		t.Iteration = 1
	}
	if err := t.Validate(); err != nil {
		return Task{}, err
	}
	switch t.Status {
	case StatusPlanned, StatusPublished, StatusEnqueued:
	default:
		return Task{}, fmt.Errorf("task: Enqueue accepts status ∈ {planned, published, enqueued}, got %q", t.Status)
	}
	if t.ID == "" {
		t.ID = NewID()
	}
	t.CreatedAt = now
	if err := s.write(ctx, t); err != nil {
		return Task{}, err
	}
	emitStatus(ctx, s.publisher, t)
	return t, nil
}

// GetByID implements Store for SurrealStore.
func (s *SurrealStore) GetByID(ctx context.Context, id string, memberships []string) (Task, error) {
	if memberships != nil && len(memberships) == 0 {
		return Task{}, ErrNotFound
	}
	conds := []string{"id = $rid"}
	vars := map[string]any{"rid": surrealmodels.NewRecordID("tasks", id)}
	if memberships != nil {
		conds = append(conds, "workspace_id IN $memberships")
		vars["memberships"] = memberships
	}
	sql := fmt.Sprintf("SELECT %s FROM tasks WHERE %s LIMIT 1", selectCols, strings.Join(conds, " AND "))
	results, err := surrealdb.Query[[]Task](ctx, s.db, sql, vars)
	if err != nil {
		return Task{}, fmt.Errorf("task: select: %w", err)
	}
	if results == nil || len(*results) == 0 || len((*results)[0].Result) == 0 {
		return Task{}, ErrNotFound
	}
	return (*results)[0].Result[0], nil
}

// List implements Store for SurrealStore.
func (s *SurrealStore) List(ctx context.Context, opts ListOptions, memberships []string) ([]Task, error) {
	if memberships != nil && len(memberships) == 0 {
		return []Task{}, nil
	}
	conds := []string{}
	vars := map[string]any{}
	if opts.Origin != "" {
		conds = append(conds, "origin = $origin")
		vars["origin"] = opts.Origin
	}
	if opts.Status != "" {
		conds = append(conds, "status = $status")
		vars["status"] = opts.Status
	} else if !opts.IncludeArchived {
		conds = append(conds, "status != $archived")
		vars["archived"] = StatusArchived
	}
	if opts.Priority != "" {
		conds = append(conds, "priority = $priority")
		vars["priority"] = opts.Priority
	}
	if opts.ClaimedBy != "" {
		conds = append(conds, "claimed_by = $claimed_by")
		vars["claimed_by"] = opts.ClaimedBy
	}
	if opts.ContractInstanceID != "" {
		conds = append(conds, "contract_instance_id = $contract_instance_id")
		vars["contract_instance_id"] = opts.ContractInstanceID
	}
	if opts.Kind != "" {
		conds = append(conds, "kind = $kind")
		vars["kind"] = opts.Kind
	}
	if memberships != nil {
		conds = append(conds, "workspace_id IN $memberships")
		vars["memberships"] = memberships
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	limit := ""
	if opts.Limit > 0 {
		limit = fmt.Sprintf(" LIMIT %d", opts.Limit)
	}
	sql := fmt.Sprintf("SELECT %s FROM tasks %s ORDER BY created_at ASC%s", selectCols, where, limit)
	results, err := surrealdb.Query[[]Task](ctx, s.db, sql, vars)
	if err != nil {
		return nil, fmt.Errorf("task: list: %w", err)
	}
	if results == nil || len(*results) == 0 {
		return []Task{}, nil
	}
	return (*results)[0].Result, nil
}

// Claim implements Store for SurrealStore with an atomic UPDATE ...
// RETURN AFTER. The query selects the highest-priority oldest-queued
// task visible to workspaceIDs, transitions it to claimed, and returns
// the mutated row. Under concurrency SurrealDB serialises the UPDATE so
// exactly one caller wins per candidate row.
func (s *SurrealStore) Claim(ctx context.Context, workerID string, workspaceIDs []string, now time.Time) (Task, error) {
	if workerID == "" {
		return Task{}, fmt.Errorf("task: worker_id required")
	}
	if len(workspaceIDs) == 0 {
		return Task{}, ErrNoTaskAvailable
	}
	// SurrealDB's UPDATE ... WHERE ... does not support ORDER BY +
	// LIMIT on the WHERE clause directly; we pick via a SELECT then
	// UPDATE with the resolved id. Ordering is created_at ASC (FIFO)
	// only; SurrealDB's ORDER BY does not accept the priority enum
	// idiom natively, so priority-aware dispatch is a follow-up
	// optimisation (feature-order:9.3 dispatcher will layer that in
	// via a priority_rank column or bucketed queries). MemoryStore
	// already enforces priority order for unit-test parity.
	// SurrealDB v3's parser needs the ORDER BY field to appear in the
	// SELECT list — including created_at here keeps it happy.
	selectSQL := "SELECT meta::id(id) AS id, workspace_id, created_at FROM tasks WHERE status IN $statuses AND workspace_id IN $memberships ORDER BY created_at LIMIT 1"
	type head struct {
		ID          string    `json:"id"`
		WorkspaceID string    `json:"workspace_id"`
		CreatedAt   time.Time `json:"created_at"`
	}
	selectRes, err := surrealdb.Query[[]head](ctx, s.db, selectSQL, map[string]any{
		"statuses":    []string{StatusPublished, StatusEnqueued},
		"memberships": workspaceIDs,
	})
	if err != nil {
		return Task{}, fmt.Errorf("task: claim select: %w", err)
	}
	if selectRes == nil || len(*selectRes) == 0 || len((*selectRes)[0].Result) == 0 {
		return Task{}, ErrNoTaskAvailable
	}
	id := (*selectRes)[0].Result[0].ID

	// Conditional UPDATE: only transitions when the row is still
	// enqueued. Concurrent callers racing on the same id get an empty
	// Result from the losing UPDATE; they retry the SELECT.
	updateSQL := fmt.Sprintf(
		"UPDATE $rid SET status = $new, claimed_by = $by, claimed_at = $at WHERE status IN $oldset RETURN %s",
		selectCols,
	)
	updateRes, err := surrealdb.Query[[]Task](ctx, s.db, updateSQL, map[string]any{
		"rid":    surrealmodels.NewRecordID("tasks", id),
		"new":    StatusClaimed,
		"oldset": []string{StatusPublished, StatusEnqueued},
		"by":     workerID,
		"at":     now,
	})
	if err != nil {
		return Task{}, fmt.Errorf("task: claim update: %w", err)
	}
	if updateRes == nil || len(*updateRes) == 0 || len((*updateRes)[0].Result) == 0 {
		// Lost the race; caller retries.
		return Task{}, ErrNoTaskAvailable
	}
	claimed := (*updateRes)[0].Result[0]
	emitStatus(ctx, s.publisher, claimed)
	return claimed, nil
}

// ClaimByID implements Store for SurrealStore. Mirrors Claim's
// conditional UPDATE: the query only transitions when the row is
// still enqueued, so two callers racing on the same id end with one
// winner and one ErrNoTaskAvailable.
func (s *SurrealStore) ClaimByID(ctx context.Context, id, workerID string, now time.Time, memberships []string) (Task, error) {
	if workerID == "" {
		return Task{}, fmt.Errorf("task: worker_id required")
	}
	if memberships != nil && len(memberships) == 0 {
		return Task{}, ErrNotFound
	}
	conds := []string{"status IN $oldset"}
	vars := map[string]any{
		"rid":    surrealmodels.NewRecordID("tasks", id),
		"new":    StatusClaimed,
		"oldset": []string{StatusPublished, StatusEnqueued},
		"by":     workerID,
		"at":     now,
	}
	if memberships != nil {
		conds = append(conds, "workspace_id IN $memberships")
		vars["memberships"] = memberships
	}
	updateSQL := fmt.Sprintf(
		"UPDATE $rid SET status = $new, claimed_by = $by, claimed_at = $at WHERE %s RETURN %s",
		strings.Join(conds, " AND "), selectCols,
	)
	updateRes, err := surrealdb.Query[[]Task](ctx, s.db, updateSQL, vars)
	if err != nil {
		return Task{}, fmt.Errorf("task: claim_by_id: %w", err)
	}
	if updateRes == nil || len(*updateRes) == 0 || len((*updateRes)[0].Result) == 0 {
		return Task{}, ErrNoTaskAvailable
	}
	claimed := (*updateRes)[0].Result[0]
	emitStatus(ctx, s.publisher, claimed)
	return claimed, nil
}

// Close implements Store for SurrealStore.
func (s *SurrealStore) Close(ctx context.Context, id, outcome string, now time.Time, memberships []string) (Task, error) {
	if _, ok := validOutcomes[outcome]; !ok {
		return Task{}, fmt.Errorf("task: invalid outcome %q", outcome)
	}
	t, err := s.GetByID(ctx, id, memberships)
	if err != nil {
		return Task{}, err
	}
	if !ValidTransition(t.Status, StatusClosed) {
		return Task{}, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, t.Status, StatusClosed)
	}
	t.Status = StatusClosed
	t.Outcome = outcome
	completed := now
	t.CompletedAt = &completed
	if err := s.write(ctx, t); err != nil {
		return Task{}, err
	}
	emitStatus(ctx, s.publisher, t)
	return t, nil
}

// Reclaim implements Store for SurrealStore.
func (s *SurrealStore) Reclaim(ctx context.Context, id, reason string, now time.Time, memberships []string) (Task, error) {
	t, err := s.GetByID(ctx, id, memberships)
	if err != nil {
		return Task{}, err
	}
	if !ValidTransition(t.Status, StatusEnqueued) {
		return Task{}, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, t.Status, StatusEnqueued)
	}
	t.Status = StatusEnqueued
	t.ClaimedBy = ""
	t.ClaimedAt = nil
	t.ReclaimCount++
	if err := s.write(ctx, t); err != nil {
		return Task{}, err
	}
	emitStatus(ctx, s.publisher, t)
	return t, nil
}

// Archive implements Store for SurrealStore.
func (s *SurrealStore) Archive(ctx context.Context, id string, now time.Time, memberships []string) (Task, error) {
	t, err := s.GetByID(ctx, id, memberships)
	if err != nil {
		return Task{}, err
	}
	if t.Status == StatusArchived {
		return t, nil
	}
	if !ValidTransition(t.Status, StatusArchived) {
		return Task{}, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, t.Status, StatusArchived)
	}
	t.Status = StatusArchived
	if err := s.write(ctx, t); err != nil {
		return Task{}, err
	}
	emitStatus(ctx, s.publisher, t)
	return t, nil
}

// ListExpiring implements Store for SurrealStore.
func (s *SurrealStore) ListExpiring(ctx context.Context, now time.Time, multiplier float64, memberships []string) ([]Task, error) {
	// Fetch all claimed/in_flight tasks in the caller's workspaces and
	// filter in-process. Simpler than encoding the expected_duration *
	// multiplier comparison in SurrealDB SQL; the in-flight set is
	// bounded by worker concurrency per workspace, so the linear scan
	// cost is acceptable.
	if memberships != nil && len(memberships) == 0 {
		return []Task{}, nil
	}
	conds := []string{"status IN [$claimed, $in_flight]"}
	vars := map[string]any{"claimed": StatusClaimed, "in_flight": StatusInFlight}
	if memberships != nil {
		conds = append(conds, "workspace_id IN $memberships")
		vars["memberships"] = memberships
	}
	sql := fmt.Sprintf("SELECT %s FROM tasks WHERE %s", selectCols, strings.Join(conds, " AND "))
	results, err := surrealdb.Query[[]Task](ctx, s.db, sql, vars)
	if err != nil {
		return nil, fmt.Errorf("task: list expiring: %w", err)
	}
	if results == nil || len(*results) == 0 {
		return []Task{}, nil
	}
	out := make([]Task, 0)
	for _, t := range (*results)[0].Result {
		if t.ExpectedDuration <= 0 || t.ClaimedAt == nil {
			continue
		}
		budget := time.Duration(float64(t.ExpectedDuration) * multiplier)
		if now.Sub(*t.ClaimedAt) <= budget {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// Publish implements Store for SurrealStore.
func (s *SurrealStore) Publish(ctx context.Context, id string, now time.Time, memberships []string) (Task, error) {
	t, err := s.GetByID(ctx, id, memberships)
	if err != nil {
		return Task{}, err
	}
	if !ValidTransition(t.Status, StatusPublished) {
		return Task{}, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, t.Status, StatusPublished)
	}
	t.Status = StatusPublished
	if err := s.write(ctx, t); err != nil {
		return Task{}, err
	}
	emitStatus(ctx, s.publisher, t)
	return t, nil
}

// Save implements Store for SurrealStore — generic upsert used by
// migrations that mutate fields outside the lifecycle helpers.
func (s *SurrealStore) Save(ctx context.Context, t Task, now time.Time) error {
	if err := t.Validate(); err != nil {
		return err
	}
	return s.write(ctx, t)
}

func (s *SurrealStore) write(ctx context.Context, t Task) error {
	sql := "UPSERT $rid CONTENT $doc"
	vars := map[string]any{
		"rid": surrealmodels.NewRecordID("tasks", t.ID),
		"doc": t,
	}
	if _, err := surrealdb.Query[[]Task](ctx, s.db, sql, vars); err != nil {
		return fmt.Errorf("task: upsert: %w", err)
	}
	return nil
}

// BackfillIteration stamps Iteration on rows whose value is zero or
// missing. The lookup callback resolves a contract_instance_id to its
// 1-based lap number among CIs of the same contract_name on the same
// story; zero means the caller couldn't resolve and the row is skipped.
// Idempotent: rows with Iteration > 0 are untouched. sty_78ddc67b.
func (s *SurrealStore) BackfillIteration(ctx context.Context, lookup func(ciID string) int) (int, error) {
	if lookup == nil {
		return 0, nil
	}
	sql := fmt.Sprintf(
		"SELECT %s FROM tasks WHERE iteration IS NONE OR iteration = 0 OR iteration = NONE",
		selectCols,
	)
	results, err := surrealdb.Query[[]Task](ctx, s.db, sql, map[string]any{})
	if err != nil {
		return 0, fmt.Errorf("task: backfill iteration select: %w", err)
	}
	if results == nil || len(*results) == 0 {
		return 0, nil
	}
	stamped := 0
	for _, t := range (*results)[0].Result {
		iter := 1
		if t.ContractInstanceID != "" {
			if n := lookup(t.ContractInstanceID); n > 0 {
				iter = n
			}
		}
		t.Iteration = iter
		if err := s.write(ctx, t); err != nil {
			return stamped, err
		}
		stamped++
	}
	return stamped, nil
}

// Compile-time assertion.
var _ Store = (*SurrealStore)(nil)
