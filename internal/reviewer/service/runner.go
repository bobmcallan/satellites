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

// CommitFn applies the reviewer's verdict against the substrate.
// Bound by main.go to mcpserver.Server.CommitReviewVerdict so the
// service and the MCP-facing contract_review_close handler share one
// commit code path.
//
// The service passes actor=ServiceUserID and memberships=nil — the
// service is a system-identity worker that operates cross-workspace.
type CommitFn func(
	ctx context.Context,
	ciID, verdict, rationale, reviewTaskID, actor string,
	now time.Time,
	memberships []string,
) error

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
	commit    CommitFn
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
	Commit    CommitFn
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
	if deps.Commit == nil {
		return nil, errors.New("reviewer/service: commit fn is required")
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
		commit:    deps.Commit,
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

// reviewTaskPayload is the JSON shape close_handlers.go's
// enqueueReviewTask writes onto the task's Payload field.
type reviewTaskPayload struct {
	ContractInstanceID string `json:"contract_instance_id"`
	ContractName       string `json:"contract_name"`
	StoryID            string `json:"story_id"`
	CloseLedgerID      string `json:"close_ledger_id"`
	EvidenceLedgerID   string `json:"evidence_ledger_id"`
}

// processClaimed assembles the review packet, runs the reviewer, and
// commits the verdict via s.commit. Errors are recorded as a rejected
// verdict carrying the failure rationale so the CI doesn't sit
// pending_review forever.
func (s *Service) processClaimed(ctx context.Context, t task.Task) {
	var payload reviewTaskPayload
	if len(t.Payload) > 0 {
		_ = json.Unmarshal(t.Payload, &payload)
	}
	if payload.ContractInstanceID == "" {
		s.commitFailure(ctx, "", "review-task payload missing contract_instance_id", t.ID, "task: payload missing ci id")
		return
	}
	ci, err := s.contracts.GetByID(ctx, payload.ContractInstanceID, nil)
	if err != nil {
		s.commitFailure(ctx, payload.ContractInstanceID, fmt.Sprintf("ci %s unresolvable: %v", payload.ContractInstanceID, err), t.ID, "ci lookup failed")
		return
	}
	contractDoc, err := s.docs.GetByID(ctx, ci.ContractID, nil)
	if err != nil {
		s.commitFailure(ctx, ci.ID, fmt.Sprintf("contract doc %s unresolvable: %v", ci.ContractID, err), t.ID, "contract doc lookup failed")
		return
	}

	evidenceMD := ""
	if payload.EvidenceLedgerID != "" {
		if row, err := s.ledger.GetByID(ctx, payload.EvidenceLedgerID, nil); err == nil {
			evidenceMD = row.Content
		}
	}
	if evidenceMD == "" && payload.CloseLedgerID != "" {
		// Fall back to the close-request markdown when no separate
		// evidence row was written.
		if row, err := s.ledger.GetByID(ctx, payload.CloseLedgerID, nil); err == nil {
			evidenceMD = row.Content
		}
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

	if err := s.commit(ctx, ci.ID, outcome, rationale, t.ID, s.cfg.UserID, s.nowUTC(), nil); err != nil {
		s.logWarn("reviewer service commit failed", err, map[string]string{
			"ci_id":   ci.ID,
			"task_id": t.ID,
			"verdict": outcome,
		})
		return
	}
	if s.logger != nil {
		s.logger.Info().
			Str("ci_id", ci.ID).
			Str("task_id", t.ID).
			Str("verdict", outcome).
			Str("contract", ci.ContractName).
			Msg("reviewer service committed verdict")
	}
}

// lookupReviewerRubric mirrors close_handlers.go's
// lookupReviewerAgentBody — develop CIs are reviewed against the
// development_reviewer body; everything else against story_reviewer.
// Empty when neither doc resolves.
func (s *Service) lookupReviewerRubric(ctx context.Context, contractName string) string {
	agentName := "story_reviewer"
	if contractName == "develop" {
		agentName = "development_reviewer"
	}
	rows, err := s.docs.List(ctx, document.ListOptions{
		Type:  document.TypeAgent,
		Scope: document.ScopeSystem,
	}, nil)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.Status == document.StatusActive && r.Name == agentName {
			return r.Body
		}
	}
	return ""
}

// commitFailure rejects the CI with the given rationale and closes
// the originating task with outcome=failure. Used when the service
// can't reach a real reviewer verdict (CI gone, contract doc
// unreadable, gemini error). Tolerant of commit failures — the
// substrate watchdog will eventually reclaim the task.
func (s *Service) commitFailure(ctx context.Context, ciID, rationale, taskID, logReason string) {
	if s.logger != nil {
		s.logger.Warn().
			Str("ci_id", ciID).
			Str("task_id", taskID).
			Str("reason", logReason).
			Str("rationale", rationale).
			Msg("reviewer service rejecting (failure path)")
	}
	if ciID == "" {
		// No CI to reject — close the task as failure so the queue
		// doesn't loop on it.
		if s.tasks != nil && taskID != "" {
			_, _ = s.tasks.Close(ctx, taskID, task.OutcomeFailure, s.nowUTC(), nil)
		}
		return
	}
	if err := s.commit(ctx, ciID, reviewer.VerdictRejected, rationale, taskID, s.cfg.UserID, s.nowUTC(), nil); err != nil {
		s.logWarn("reviewer service failure-path commit failed", err, map[string]string{
			"ci_id":   ciID,
			"task_id": taskID,
		})
		// Best-effort task close so the queue isn't stuck on the
		// failed-commit task.
		if s.tasks != nil && taskID != "" {
			_, _ = s.tasks.Close(ctx, taskID, task.OutcomeFailure, s.nowUTC(), nil)
		}
	}
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
