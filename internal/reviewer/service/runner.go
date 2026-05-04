package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ternarybob/arbor"

	"github.com/bobmcallan/satellites/internal/contract"
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
// Run (blocks until the supplied context is cancelled).
type Service struct {
	cfg       Config
	tasks     task.Store
	contracts contract.Store
	docs      document.Store
	ledger    ledger.Store
	reviewer  reviewer.Reviewer
	logger    arbor.ILogger
	nowUTC    func() time.Time
}

// Deps bundles the service's required collaborators.
type Deps struct {
	Tasks     task.Store
	Contracts contract.Store
	Docs      document.Store
	Ledger    ledger.Store
	Reviewer  reviewer.Reviewer
	Logger    arbor.ILogger
	Now       func() time.Time // injectable clock for tests
}

// New constructs a Service and validates the deps. Returns an error
// when a required dep is nil — failures here are fatal at boot, not
// runtime, so the caller can short-circuit before starting the
// goroutine.
func New(cfg Config, deps Deps) (*Service, error) {
	if deps.Tasks == nil {
		return nil, errors.New("reviewer/service: tasks store is required")
	}
	if deps.Contracts == nil {
		return nil, errors.New("reviewer/service: contracts store is required")
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
		cfg:       cfg,
		tasks:     deps.Tasks,
		contracts: deps.Contracts,
		docs:      deps.Docs,
		ledger:    deps.Ledger,
		reviewer:  deps.Reviewer,
		logger:    deps.Logger,
		nowUTC:    now,
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
// commits the verdict via s.commit. Errors are recorded as a rejected
// verdict carrying the failure rationale so the CI doesn't sit
// pending_review forever.
//
// sty_c6d76a5b: tasks no longer carry a Payload pointing at close /
// evidence ledger rows. The reviewer sources its inputs by walking
// the ledger filtered by ContractInstanceID and picking the most
// recent kind:evidence (or kind:close-request as fallback).
func (s *Service) processClaimed(ctx context.Context, t task.Task) {
	if t.ContractInstanceID == "" {
		s.commitFailure(ctx, "", "review task missing contract_instance_id", t.ID, "task: missing ci linkage")
		return
	}
	ci, err := s.contracts.GetByID(ctx, t.ContractInstanceID, nil)
	if err != nil {
		s.commitFailure(ctx, t.ContractInstanceID, fmt.Sprintf("ci %s unresolvable: %v", t.ContractInstanceID, err), t.ID, "ci lookup failed")
		return
	}
	contractDoc, err := s.docs.GetByID(ctx, ci.ContractID, nil)
	if err != nil {
		s.commitFailure(ctx, ci.ID, fmt.Sprintf("contract doc %s unresolvable: %v", ci.ContractID, err), t.ID, "contract doc lookup failed")
		return
	}

	evidenceMD := s.findCIArtifact(ctx, ci, "kind:evidence")
	if evidenceMD == "" {
		// Fall back to the close-request markdown when no separate
		// evidence row was written.
		evidenceMD = s.findCIArtifact(ctx, ci, "kind:close-request")
	}
	rubric := s.lookupReviewerRubric(ctx, ci.ContractName)

	req := reviewer.Request{
		ContractID:       contractDoc.ID,
		ContractName:     contractDoc.Name,
		AgentInstruction: contractDoc.Body,
		ReviewerRubric:   rubric,
		EvidenceMarkdown: evidenceMD,
		ACScope:          ci.ACScope,
	}
	verdict, _, err := s.reviewer.Review(ctx, req)
	if err != nil {
		s.commitFailure(ctx, ci.ID, fmt.Sprintf("reviewer error: %v", err), t.ID, "reviewer call failed")
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
			// story_224621bd: post review-question ledger rows so
			// contract_respond can address them. Each question gets its
			// own row tagged kind:review-question, scoped to the CI, so
			// findLatestReviewQuestion (close_handlers.go:584) can
			// resolve the latest unresolved question.
			s.writeReviewQuestionRows(ctx, ci, verdict.ReviewQuestions)
			rationale = strings.TrimSpace(rationale + "\n\nReview questions:\n- " + strings.Join(verdict.ReviewQuestions, "\n- "))
		}
	default:
		outcome = reviewer.VerdictRejected
		rationale = fmt.Sprintf("reviewer returned unexpected outcome %q; rejecting (rationale: %s)", verdict.Outcome, rationale)
	}

	s.commitVerdict(ctx, t, ci, outcome, rationale)
}

// commitVerdict is the new task-centric commit path (sty_c6d76a5b
// slice A): write a kind:verdict ledger row tagged to the review task,
// close the review task with the matching outcome, and on rejection
// spawn a successor kind:work + paired planned-review task pair so the
// rejection-loop continues as a task chain. CI status is also flipped
// (passed/failed) when ContractInstanceID is set — transitional
// support for the legacy contract_close path until slice E removes
// contract_instance entirely. Replaces the prior CommitFn injection
// that wrapped mcpserver.Server.CommitReviewVerdict.
func (s *Service) commitVerdict(ctx context.Context, reviewTask task.Task, ci contract.ContractInstance, outcome, rationale string) {
	now := s.nowUTC()

	if _, err := s.writeVerdictLedgerRow(ctx, reviewTask, ci, outcome, rationale, now); err != nil {
		s.logWarn("reviewer service verdict ledger append failed", err, map[string]string{
			"task_id": reviewTask.ID,
			"ci_id":   ci.ID,
			"verdict": outcome,
		})
		// Best-effort task close so the queue isn't stuck on the
		// failed-write task.
		_, _ = s.tasks.Close(ctx, reviewTask.ID, task.OutcomeFailure, now, nil)
		return
	}

	// Transitional CI status flip — keeps the legacy contract_close
	// path's downstream consumers (contract_next gate, portal CI
	// views) functional until slice E removes contract_instance.
	if ci.ID != "" {
		target := contract.StatusPassed
		if outcome == reviewer.VerdictRejected {
			target = contract.StatusFailed
		}
		if _, err := s.contracts.UpdateStatus(ctx, ci.ID, target, s.cfg.UserID, now, nil); err != nil {
			s.logWarn("reviewer service ci status flip failed", err, map[string]string{
				"ci_id":   ci.ID,
				"task_id": reviewTask.ID,
				"target":  target,
			})
		}
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
			Str("ci_id", ci.ID).
			Str("task_id", reviewTask.ID).
			Str("verdict", outcome).
			Str("contract", ci.ContractName).
			Msg("reviewer service committed verdict")
	}
}

// writeVerdictLedgerRow records the reviewer's verdict as a
// kind:verdict ledger row tagged to the review task (task_id:<id>).
// When the review task carries a CI binding the row is also stamped
// with the CI's contract id + ci:<id> tag for cross-referencing
// while CIs still exist.
func (s *Service) writeVerdictLedgerRow(ctx context.Context, reviewTask task.Task, ci contract.ContractInstance, outcome, rationale string, now time.Time) (string, error) {
	tags := []string{"kind:verdict", "task_id:" + reviewTask.ID}
	if ci.ContractName != "" {
		tags = append(tags, "phase:"+ci.ContractName)
	}
	if ci.ID != "" {
		tags = append(tags, "ci:"+ci.ID)
	}
	structured, _ := json.Marshal(map[string]any{
		"verdict":        outcome,
		"rationale":      rationale,
		"review_task_id": reviewTask.ID,
		"mode":           reviewer.ModeTask,
	})
	entry := ledger.LedgerEntry{
		WorkspaceID: workspaceFor(reviewTask, ci),
		ProjectID:   projectFor(reviewTask, ci),
		StoryID:     ledger.StringPtr(storyIDFor(reviewTask, ci)),
		Type:        ledger.TypeVerdict,
		Tags:        tags,
		Content:     rationale,
		Structured:  structured,
		Durability:  ledger.DurabilityDurable,
		SourceType:  ledger.SourceSystem,
		Status:      ledger.StatusActive,
		CreatedBy:   s.cfg.UserID,
	}
	if ci.ID != "" {
		entry.ContractID = ledger.StringPtr(ci.ID)
	}
	row, err := s.ledger.Append(ctx, entry, now)
	if err != nil {
		return "", err
	}
	return row.ID, nil
}

// spawnSuccessorWorkPair emits a fresh kind:work task with PriorTaskID
// pointing at the rejected work task, plus its paired planned
// kind:review sibling. The successor work lands at status=published
// (immediately claimable); the review at status=planned (gated until
// the work closes). Returns the successor work task id, or empty when
// the review task lacks a ParentTaskID anchor (legacy review without
// the work-task linkage — manual contract_cancel remains the recovery
// path for those).
func (s *Service) spawnSuccessorWorkPair(ctx context.Context, reviewTask task.Task, now time.Time) (string, error) {
	if reviewTask.ParentTaskID == "" {
		return "", nil
	}
	parentWork, err := s.tasks.GetByID(ctx, reviewTask.ParentTaskID, nil)
	if err != nil {
		return "", fmt.Errorf("lookup parent work task %s: %w", reviewTask.ParentTaskID, err)
	}
	successor := task.Task{
		WorkspaceID:        parentWork.WorkspaceID,
		ProjectID:          parentWork.ProjectID,
		StoryID:            parentWork.StoryID,
		ContractInstanceID: parentWork.ContractInstanceID,
		Kind:               task.KindWork,
		Action:             parentWork.Action,
		AgentID:            parentWork.AgentID,
		Description:        fmt.Sprintf("retry %s after rejected review", parentWork.Action),
		ParentTaskID:       parentWork.ParentTaskID,
		PriorTaskID:        parentWork.ID,
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
		Status:             task.StatusPublished,
	}
	created, err := s.tasks.Enqueue(ctx, successor, now)
	if err != nil {
		return "", fmt.Errorf("enqueue successor work task: %w", err)
	}
	pairedReview := task.Task{
		WorkspaceID:        parentWork.WorkspaceID,
		ProjectID:          parentWork.ProjectID,
		StoryID:            parentWork.StoryID,
		ContractInstanceID: parentWork.ContractInstanceID,
		Kind:               task.KindReview,
		Action:             parentWork.Action,
		AgentID:            reviewTask.AgentID,
		Description:        fmt.Sprintf("review retry of %s", parentWork.Action),
		ParentTaskID:       created.ID,
		Origin:             task.OriginStoryStage,
		Priority:           task.PriorityMedium,
		Status:             task.StatusPlanned,
	}
	if _, err := s.tasks.Enqueue(ctx, pairedReview, now); err != nil {
		return created.ID, fmt.Errorf("enqueue paired review for successor: %w", err)
	}
	return created.ID, nil
}

// workspaceFor / projectFor / storyIDFor pick the best non-empty
// scoping value across (review task, ci). Review tasks created via
// story_task_submit carry the story scoping directly; legacy tasks
// created via contract_close inherit theirs from the bound CI.
func workspaceFor(t task.Task, ci contract.ContractInstance) string {
	if t.WorkspaceID != "" {
		return t.WorkspaceID
	}
	return ci.WorkspaceID
}

func projectFor(t task.Task, ci contract.ContractInstance) string {
	if t.ProjectID != "" {
		return t.ProjectID
	}
	return ci.ProjectID
}

func storyIDFor(t task.Task, ci contract.ContractInstance) string {
	if t.StoryID != "" {
		return t.StoryID
	}
	return ci.StoryID
}

// findCIArtifact returns the most recent ledger row tagged kindTag
// scoped to ci's project + contract_id. Empty when no row matches.
// Used by processClaimed to source close-request + evidence markdown
// without relying on a per-task Payload (sty_c6d76a5b).
func (s *Service) findCIArtifact(ctx context.Context, ci contract.ContractInstance, kindTag string) string {
	rows, err := s.ledger.List(ctx, ci.ProjectID, ledger.ListOptions{
		ContractID: ci.ID,
		Tags:       []string{kindTag},
	}, nil)
	if err != nil || len(rows) == 0 {
		return ""
	}
	// ledger.List returns rows in newest-first order; pick the latest.
	return rows[0].Content
}

// lookupReviewerRubric returns the body of the system-scope agent
// that reviews the given contract. Resolution order (sty_c6d76a5b):
//
//  1. First active agent whose AgentSettings.Reviews contains
//     `contract:<contractName>`.
//  2. Legacy name-match fallback: `develop` → `development_reviewer`,
//     else → `story_reviewer`.
//
// Empty when neither resolution finds a doc. Mirrors the lookup in
// internal/mcpserver/close_handlers.go::findReviewerAgent — kept in
// lockstep until that helper is promoted to a shared package.
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
// the service couldn't reach a real reviewer verdict (CI gone,
// contract doc unreadable, gemini error, missing CI linkage). Goes
// through commitVerdict so the same ledger-row + task-close + spawn
// behavior applies — the verdict is just synthesised by the service
// rather than the reviewer.
func (s *Service) commitFailure(ctx context.Context, ciID, rationale, taskID, logReason string) {
	if s.logger != nil {
		s.logger.Warn().
			Str("ci_id", ciID).
			Str("task_id", taskID).
			Str("reason", logReason).
			Str("rationale", rationale).
			Msg("reviewer service rejecting (failure path)")
	}
	reviewTask, gerr := s.tasks.GetByID(ctx, taskID, nil)
	if gerr != nil {
		// Task vanished mid-process — nothing to record. The reviewer
		// already received the failure rationale via the log line above.
		return
	}
	ci := contract.ContractInstance{}
	if ciID != "" {
		if found, err := s.contracts.GetByID(ctx, ciID, nil); err == nil {
			ci = found
		}
	}
	s.commitVerdict(ctx, reviewTask, ci, reviewer.VerdictRejected, rationale)
}

// writeReviewQuestionRows records each question as its own
// kind:review-question ledger row scoped to the CI. story_224621bd:
// addresses the AC requirement that needs_more verdicts post review-
// question rows that contract_respond (close_handlers.go) can find via
// findLatestReviewQuestion.
func (s *Service) writeReviewQuestionRows(ctx context.Context, ci contract.ContractInstance, questions []string) {
	if s.ledger == nil {
		return
	}
	now := s.nowUTC()
	for i, q := range questions {
		structured, _ := json.Marshal(map[string]any{
			"index":    i,
			"question": q,
		})
		_, err := s.ledger.Append(ctx, ledger.LedgerEntry{
			WorkspaceID: ci.WorkspaceID,
			ProjectID:   ci.ProjectID,
			StoryID:     ledger.StringPtr(ci.StoryID),
			ContractID:  ledger.StringPtr(ci.ID),
			Type:        ledger.TypeDecision,
			Tags:        []string{"kind:review-question", "phase:" + ci.ContractName},
			Content:     q,
			Structured:  structured,
			Durability:  ledger.DurabilityDurable,
			SourceType:  ledger.SourceSystem,
			Status:      ledger.StatusActive,
			CreatedBy:   s.cfg.UserID,
		}, now)
		if err != nil {
			s.logWarn("reviewer service review-question append failed", err, map[string]string{
				"ci_id": ci.ID,
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
