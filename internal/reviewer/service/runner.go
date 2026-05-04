package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ternarybob/arbor"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/task"
)

// Config carries the reviewer service's wiring.
type Config struct {
	// SessionID is the registry session id the reviewer service runs
	// under. Defaults to ServiceSessionID.
	SessionID string

	// UserID is the system identity that owns SessionID. Defaults to
	// ServiceUserID.
	UserID string

	// WorkerID is the value stamped as ClaimedBy on every claimed
	// task. Defaults to WorkerID (the package constant).
	WorkerID string
}

// Service is the embedded reviewer worker. Construct via New, run via
// Run (blocks until the supplied context is cancelled). sty_c6d76a5b
// checkpoint 14: the contract.Store dependency is gone — the service
// reads everything it needs from the task + the ledger.
type Service struct {
	cfg      Config
	tasks    task.Store
	docs     document.Store
	ledger   ledger.Store
	reviewer reviewer.Reviewer
	logger   arbor.ILogger
	nowUTC   func() time.Time
}

// Deps bundles the service's required collaborators.
type Deps struct {
	Tasks    task.Store
	Docs     document.Store
	Ledger   ledger.Store
	Reviewer reviewer.Reviewer
	Logger   arbor.ILogger
	Now      func() time.Time // injectable clock for tests
}

// New constructs a Service and validates the deps. Returns an error
// when a required dep is nil — failures here are fatal at boot, not
// runtime, so the caller can short-circuit before starting the
// goroutine.
func New(cfg Config, deps Deps) (*Service, error) {
	if deps.Tasks == nil {
		return nil, errors.New("reviewer/service: tasks store is required")
	}
	if deps.Docs == nil {
		return nil, errors.New("reviewer/service: docs store is required")
	}
	if deps.Ledger == nil {
		return nil, errors.New("reviewer/service: ledger store is required")
	}
	if deps.Reviewer == nil {
		return nil, errors.New("reviewer/service: reviewer is required")
	}
	if cfg.SessionID == "" {
		cfg.SessionID = ServiceSessionID
	}
	if cfg.UserID == "" {
		cfg.UserID = ServiceUserID
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = WorkerID
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		cfg:      cfg,
		tasks:    deps.Tasks,
		docs:     deps.Docs,
		ledger:   deps.Ledger,
		reviewer: deps.Reviewer,
		logger:   deps.Logger,
		nowUTC:   now,
	}, nil
}

// Run registers the service as a task-store listener and blocks until
// ctx is cancelled. The store delivers status events to OnEmit; the
// service filters Kind=review + claimable statuses and processes one
// task at a time. No polling — sty_c6d76a5b replaces Tick with the
// subscribe path.
//
// On startup the service drains any kind:review tasks already in the
// queue at claimable statuses — listener registrations only fire on
// future emits, so without the drain a process restart would leave
// pre-existing reviews unclaimed.
func (s *Service) Run(ctx context.Context) error {
	if s.logger != nil {
		s.logger.Info().
			Str("session_id", s.cfg.SessionID).
			Str("worker_id", s.cfg.WorkerID).
			Msg("reviewer service starting")
	}
	s.drainAtBoot(ctx)
	if attacher, ok := s.tasks.(interface {
		AddListener(task.Listener)
	}); ok {
		attacher.AddListener(s)
	} else if s.logger != nil {
		s.logger.Warn().Msg("reviewer service: task store does not expose AddListener; the service will idle until ctx is cancelled")
	}
	<-ctx.Done()
	if s.logger != nil {
		s.logger.Info().Msg("reviewer service stopping (context cancelled)")
	}
	return ctx.Err()
}

// drainAtBoot lists kind:review tasks already at a claimable status
// (pre-c6d76a5b enqueued, post-c6d76a5b published) and processes
// them. Bridges the gap between process restart and listener
// registration — without this, tasks emitted while the service was
// down would never be picked up.
func (s *Service) drainAtBoot(ctx context.Context) {
	statuses := []string{task.StatusPublished, task.StatusEnqueued}
	for _, status := range statuses {
		candidates, err := s.tasks.List(ctx, task.ListOptions{
			Status: status,
			Kind:   task.KindReview,
			Limit:  64,
		}, nil)
		if err != nil {
			s.logWarn("reviewer service drain list failed", err, map[string]string{"status": status})
			continue
		}
		for _, t := range candidates {
			s.HandleTaskEvent(ctx, t)
		}
	}
}

// OnEmit implements task.Listener. Filters for Kind=review tasks at
// claimable status, then claims-by-id and processes. Lost claim races
// + non-review tasks are silently skipped — the substrate fans every
// emit out to every listener; filtering is the listener's concern.
func (s *Service) OnEmit(ctx context.Context, t task.Task) {
	if t.Kind != task.KindReview {
		return
	}
	if !task.IsSubscriberVisible(t.Status) {
		return
	}
	// Only act on the publish/enqueue transition — not on claimed,
	// in_flight, or our own subsequent emits. Otherwise we'd loop on
	// our own claim event.
	if t.Status != task.StatusPublished && t.Status != task.StatusEnqueued {
		return
	}
	s.HandleTaskEvent(ctx, t)
}

// HandleTaskEvent claims the named task and processes it. Returns
// true when the task was claimed and processed (whether the verdict
// was accepted or rejected — both count as work done); false on
// race-loss / not-found / non-claimable. Exposed so tests can drive
// the service deterministically without registering a listener.
func (s *Service) HandleTaskEvent(ctx context.Context, t task.Task) bool {
	claimed, err := s.tasks.ClaimByID(ctx, t.ID, s.cfg.WorkerID, s.nowUTC(), nil)
	if errors.Is(err, task.ErrNoTaskAvailable) || errors.Is(err, task.ErrNotFound) {
		return false
	}
	if err != nil {
		s.logWarn("reviewer service claim failed", err, map[string]string{"task_id": t.ID})
		return false
	}
	s.processClaimed(ctx, claimed)
	return true
}

// processClaimed assembles the review packet, runs the reviewer, and
// commits the verdict. The review task carries Action (canonical
// `contract:<name>`) directly — the contract doc is resolved by name
// rather than by joining through a CI row. Evidence is sourced from
// kind:evidence ledger rows tagged with the parent work task id; the
// agent stamps that tag when it writes evidence alongside its close
// submission.
func (s *Service) processClaimed(ctx context.Context, t task.Task) {
	contractName := contractNameFromAction(t.Action)
	contractDoc := s.findContractDocByName(ctx, contractName)
	rubric := s.lookupReviewerRubric(ctx, contractName)
	evidenceMD := s.findTaskEvidence(ctx, t)

	req := reviewer.Request{
		ContractID:       contractDoc.ID,
		ContractName:     contractName,
		AgentInstruction: contractDoc.Body,
		ReviewerRubric:   rubric,
		EvidenceMarkdown: evidenceMD,
	}
	verdict, _, err := s.reviewer.Review(ctx, req)
	if err != nil {
		s.commitFailure(ctx, t, fmt.Sprintf("reviewer error: %v", err), "reviewer call failed")
		return
	}
	outcome := verdict.Outcome
	rationale := verdict.Rationale
	switch outcome {
	case reviewer.VerdictAccepted, reviewer.VerdictRejected:
		// pass through
	case reviewer.VerdictNeedsMore:
		// The task path doesn't support needs_more — the reviewer must
		// commit. Treat as rejected with the questions appended to the
		// rationale so the next iteration carries the context.
		outcome = reviewer.VerdictRejected
		if len(verdict.ReviewQuestions) > 0 {
			s.writeReviewQuestionRows(ctx, t, verdict.ReviewQuestions)
			rationale = strings.TrimSpace(rationale + "\n\nReview questions:\n- " + strings.Join(verdict.ReviewQuestions, "\n- "))
		}
	default:
		outcome = reviewer.VerdictRejected
		rationale = fmt.Sprintf("reviewer returned unexpected outcome %q; rejecting (rationale: %s)", verdict.Outcome, rationale)
	}

	s.commitVerdict(ctx, t, contractName, outcome, rationale)
}

// commitVerdict writes a kind:verdict ledger row tagged to the review
// task, closes the review task, and on rejection spawns a successor
// work + paired planned-review task pair so the rejection-loop
// continues as a task chain.
func (s *Service) commitVerdict(ctx context.Context, reviewTask task.Task, contractName, outcome, rationale string) {
	now := s.nowUTC()

	if _, err := s.writeVerdictLedgerRow(ctx, reviewTask, contractName, outcome, rationale, now); err != nil {
		s.logWarn("reviewer service verdict ledger append failed", err, map[string]string{
			"task_id": reviewTask.ID,
			"verdict": outcome,
		})
		_, _ = s.tasks.Close(ctx, reviewTask.ID, task.OutcomeFailure, now, nil)
		return
	}

	taskOutcome := task.OutcomeSuccess
	if outcome == reviewer.VerdictRejected {
		taskOutcome = task.OutcomeFailure
	}
	if _, err := s.tasks.Close(ctx, reviewTask.ID, taskOutcome, now, nil); err != nil {
		s.logWarn("reviewer service review-task close failed", err, map[string]string{
			"task_id": reviewTask.ID,
			"verdict": outcome,
		})
		return
	}

	if outcome == reviewer.VerdictRejected {
		if successorID, err := s.spawnSuccessorWorkPair(ctx, reviewTask, now); err != nil {
			s.logWarn("reviewer service successor spawn failed", err, map[string]string{
				"task_id": reviewTask.ID,
			})
		} else if s.logger != nil && successorID != "" {
			s.logger.Info().
				Str("review_task_id", reviewTask.ID).
				Str("successor_task_id", successorID).
				Msg("reviewer service spawned successor work task")
		}
	}

	if s.logger != nil {
		s.logger.Info().
			Str("task_id", reviewTask.ID).
			Str("verdict", outcome).
			Str("contract", contractName).
			Msg("reviewer service committed verdict")
	}
}

// writeVerdictLedgerRow records the reviewer's verdict as a
// kind:verdict ledger row tagged with the review task id and contract
// phase.
func (s *Service) writeVerdictLedgerRow(ctx context.Context, reviewTask task.Task, contractName, outcome, rationale string, now time.Time) (string, error) {
	tags := []string{"kind:verdict", "task_id:" + reviewTask.ID}
	if contractName != "" {
		tags = append(tags, "phase:"+contractName)
	}
	structured, _ := json.Marshal(map[string]any{
		"verdict":        outcome,
		"rationale":      rationale,
		"review_task_id": reviewTask.ID,
		"mode":           reviewer.ModeTask,
	})
	entry := ledger.LedgerEntry{
		WorkspaceID: reviewTask.WorkspaceID,
		ProjectID:   reviewTask.ProjectID,
		StoryID:     ledger.StringPtr(reviewTask.StoryID),
		Type:        ledger.TypeVerdict,
		Tags:        tags,
		Content:     rationale,
		Structured:  structured,
		Durability:  ledger.DurabilityDurable,
		SourceType:  ledger.SourceSystem,
		Status:      ledger.StatusActive,
		CreatedBy:   s.cfg.UserID,
	}
	row, err := s.ledger.Append(ctx, entry, now)
	if err != nil {
		return "", err
	}
	return row.ID, nil
}

// spawnSuccessorWorkPair emits a fresh kind:work task with PriorTaskID
// pointing at the rejected work task, plus its paired planned
// kind:review sibling. Returns the successor work task id, or empty
// when the review task lacks a ParentTaskID anchor.
func (s *Service) spawnSuccessorWorkPair(ctx context.Context, reviewTask task.Task, now time.Time) (string, error) {
	if reviewTask.ParentTaskID == "" {
		return "", nil
	}
	parentWork, err := s.tasks.GetByID(ctx, reviewTask.ParentTaskID, nil)
	if err != nil {
		return "", fmt.Errorf("lookup parent work task %s: %w", reviewTask.ParentTaskID, err)
	}
	successor := task.Task{
		WorkspaceID:  parentWork.WorkspaceID,
		ProjectID:    parentWork.ProjectID,
		StoryID:      parentWork.StoryID,
		Kind:         task.KindWork,
		Action:       parentWork.Action,
		AgentID:      parentWork.AgentID,
		Description:  fmt.Sprintf("retry %s after rejected review", parentWork.Action),
		ParentTaskID: parentWork.ParentTaskID,
		PriorTaskID:  parentWork.ID,
		Origin:       task.OriginStoryStage,
		Priority:     task.PriorityMedium,
		Status:       task.StatusPublished,
	}
	created, err := s.tasks.Enqueue(ctx, successor, now)
	if err != nil {
		return "", fmt.Errorf("enqueue successor work task: %w", err)
	}
	pairedReview := task.Task{
		WorkspaceID:  parentWork.WorkspaceID,
		ProjectID:    parentWork.ProjectID,
		StoryID:      parentWork.StoryID,
		Kind:         task.KindReview,
		Action:       parentWork.Action,
		AgentID:      reviewTask.AgentID,
		Description:  fmt.Sprintf("review retry of %s", parentWork.Action),
		ParentTaskID: created.ID,
		Origin:       task.OriginStoryStage,
		Priority:     task.PriorityMedium,
		Status:       task.StatusPlanned,
	}
	if _, err := s.tasks.Enqueue(ctx, pairedReview, now); err != nil {
		return created.ID, fmt.Errorf("enqueue paired review for successor: %w", err)
	}
	return created.ID, nil
}

// findTaskEvidence returns the most recent kind:evidence ledger row
// tagged with the parent work task's id (`task_id:<id>`). Falls back
// to the kind:close-request body when no separate evidence row was
// written. Empty when neither resolves.
func (s *Service) findTaskEvidence(ctx context.Context, reviewTask task.Task) string {
	if s.ledger == nil || reviewTask.ParentTaskID == "" || reviewTask.ProjectID == "" {
		return ""
	}
	taskTag := "task_id:" + reviewTask.ParentTaskID
	for _, kind := range []string{"kind:evidence", "kind:close-request"} {
		rows, err := s.ledger.List(ctx, reviewTask.ProjectID, ledger.ListOptions{
			Tags: []string{kind, taskTag},
		}, nil)
		if err == nil && len(rows) > 0 {
			return rows[0].Content
		}
	}
	return ""
}

// findContractDocByName resolves the active system-scope contract doc
// with the given name. Returns the empty document when nothing
// matches; the reviewer copes with empty contract bodies gracefully
// (the rubric carries the load-bearing instructions).
func (s *Service) findContractDocByName(ctx context.Context, name string) document.Document {
	if name == "" || s.docs == nil {
		return document.Document{}
	}
	rows, err := s.docs.List(ctx, document.ListOptions{
		Type:  document.TypeContract,
		Scope: document.ScopeSystem,
	}, nil)
	if err != nil {
		return document.Document{}
	}
	for _, r := range rows {
		if r.Status == document.StatusActive && r.Name == name {
			return r
		}
	}
	return document.Document{}
}

// contractNameFromAction unwraps `contract:<name>` actions into the
// bare contract name. Empty when the action is empty or doesn't carry
// the contract prefix.
func contractNameFromAction(action string) string {
	const prefix = "contract:"
	if len(action) <= len(prefix) || action[:len(prefix)] != prefix {
		return ""
	}
	return action[len(prefix):]
}

// lookupReviewerRubric returns the body of the system-scope agent
// that reviews the given contract. Resolution order (sty_c6d76a5b):
//
//  1. First active agent whose AgentSettings.Reviews contains
//     `contract:<contractName>`.
//  2. Legacy name-match fallback: `develop` → `development_reviewer`,
//     else → `story_reviewer`.
//
// Empty when neither resolution finds a doc.
func (s *Service) lookupReviewerRubric(ctx context.Context, contractName string) string {
	rows, err := s.docs.List(ctx, document.ListOptions{
		Type:  document.TypeAgent,
		Scope: document.ScopeSystem,
	}, nil)
	if err != nil {
		return ""
	}
	action := task.ContractAction(contractName)
	for _, r := range rows {
		if r.Status != document.StatusActive {
			continue
		}
		settings, perr := document.UnmarshalAgentSettings(r.Structured)
		if perr != nil {
			continue
		}
		if settings.CanReview(action) {
			return r.Body
		}
	}
	fallbackName := "story_reviewer"
	if contractName == "develop" {
		fallbackName = "development_reviewer"
	}
	for _, r := range rows {
		if r.Status == document.StatusActive && r.Name == fallbackName {
			return r.Body
		}
	}
	return ""
}

// commitFailure records a rejected verdict for the given task when
// the service couldn't reach a real reviewer verdict (gemini error,
// missing context, etc.).
func (s *Service) commitFailure(ctx context.Context, reviewTask task.Task, rationale, logReason string) {
	if s.logger != nil {
		s.logger.Warn().
			Str("task_id", reviewTask.ID).
			Str("reason", logReason).
			Str("rationale", rationale).
			Msg("reviewer service rejecting (failure path)")
	}
	contractName := contractNameFromAction(reviewTask.Action)
	s.commitVerdict(ctx, reviewTask, contractName, reviewer.VerdictRejected, rationale)
}

// writeReviewQuestionRows records each question as its own
// kind:review-question ledger row tagged with the parent work task id
// so the agent's next attempt can address them.
func (s *Service) writeReviewQuestionRows(ctx context.Context, reviewTask task.Task, questions []string) {
	if s.ledger == nil {
		return
	}
	now := s.nowUTC()
	contractName := contractNameFromAction(reviewTask.Action)
	tags := []string{"kind:review-question"}
	if contractName != "" {
		tags = append(tags, "phase:"+contractName)
	}
	if reviewTask.ParentTaskID != "" {
		tags = append(tags, "task_id:"+reviewTask.ParentTaskID)
	}
	for i, q := range questions {
		structured, _ := json.Marshal(map[string]any{
			"index":    i,
			"question": q,
		})
		_, err := s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: reviewTask.WorkspaceID,
			ProjectID:   reviewTask.ProjectID,
			StoryID:     ledger.StringPtr(reviewTask.StoryID),
			Type:        ledger.TypeDecision,
			Tags:        tags,
			Content:     q,
			Structured:  structured,
			Durability:  ledger.DurabilityDurable,
			SourceType:  ledger.SourceSystem,
			Status:      ledger.StatusActive,
			CreatedBy:   s.cfg.UserID,
		}, now)
		if err != nil {
			s.logWarn("reviewer service review-question append failed", err, map[string]string{
				"task_id": reviewTask.ID,
			})
		}
	}
}

func (s *Service) logWarn(msg string, err error, fields map[string]string) {
	if s.logger == nil {
		return
	}
	ev := s.logger.Warn()
	if err != nil {
		ev = ev.Str("error", err.Error())
	}
	for k, v := range fields {
		ev = ev.Str(k, v)
	}
	ev.Msg(msg)
}
