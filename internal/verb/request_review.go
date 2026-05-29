// story_request_review verb — the loop that gates every story-status
// transition (sty_b8c5c23f).
//
// Pipeline:
//
//   1. Read the story by id; derive story_type from the `category`.
//   2. Read the project-config document (project-scoped, name="project-config")
//      and look up the workflow_skill path for that story_type.
//   3. Read the workflow-skill file from disk (worktree-root + path) and
//      parse it through internal/workflow.
//   4. Pick the transition to run: dynamic (workflow.dynamic=true) means
//      the workflow itself is dispatched and the next status comes from
//      its return; declarative means a single named reviewer skill gates
//      the one outgoing transition with a non-empty reviewer_skill.
//   5. Mint a short-lived reviewer api-key for the subprocess.
//   6. Dispatch the gate skill via the injected GateDispatcher. Story
//      body + recent ledger arrive on stdin; reviewer key arrives in env.
//   7. On accept: patch status to next_status (reviewer-role context),
//      append a ledger row recording the accept. On reject: ledger-only.
//   8. Revoke the reviewer key.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/workflow"
)

// StoryRequestReviewRequest is the input shape. story_id is the only
// required field. worktree_root overrides the configured default for
// callers that have a per-request CWD (test fixtures); production
// callers leave it empty and let the package-level default apply.
type StoryRequestReviewRequest struct {
	StoryID      string `json:"story_id"`
	WorktreeRoot string `json:"worktree_root,omitempty"`
}

// StoryRequestReviewResponse mirrors the contract in sty_b8c5c23f: the
// gate's decision, free-form notes, and (when accepted) the new story
// status the verb just transitioned through.
type StoryRequestReviewResponse struct {
	Decision  string `json:"decision"`
	Notes     string `json:"notes,omitempty"`
	NewStatus string `json:"new_status,omitempty"`
}

// projectConfig mirrors the YAML body shape documented in
// docs/document-types-v5.md §2.
type projectConfig struct {
	StoryTypes map[string]struct {
		WorkflowSkill string `yaml:"workflow_skill"`
	} `yaml:"story_types"`
}

// workflowSkillReader resolves a workflow-skill path (as stored in
// project-config) to its bytes. Production reads from the configured
// worktree_root + path on disk; tests stub this to serve from memory.
type workflowSkillReader func(worktreeRoot, relPath string) ([]byte, error)

var skillReader workflowSkillReader = defaultSkillReader

// SetWorkflowSkillReader swaps the workflow-skill loader. Pass nil to
// restore the default disk-backed reader. Used by tests to inject an
// in-memory workflow without touching the filesystem.
func SetWorkflowSkillReader(fn func(worktreeRoot, relPath string) ([]byte, error)) {
	if fn == nil {
		skillReader = defaultSkillReader
		return
	}
	skillReader = fn
}

// defaultSkillReader is the on-disk loader: joins worktreeRoot + path
// and ReadFile's the result. Paths that escape the worktree root via
// `..` are rejected so a malicious project-config can't pull a file
// from anywhere on the host.
func defaultSkillReader(worktreeRoot, relPath string) ([]byte, error) {
	if strings.TrimSpace(relPath) == "" {
		return nil, fmt.Errorf("workflow skill path empty")
	}
	clean := filepath.Clean(relPath)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return nil, fmt.Errorf("workflow skill path %q must be relative to worktree root", relPath)
	}
	if worktreeRoot == "" {
		return os.ReadFile(clean)
	}
	return os.ReadFile(filepath.Join(worktreeRoot, clean))
}

// defaultWorktreeRoot is the package-level fallback when a request omits
// worktree_root. Server boot sets this from server config; CLI in-process
// callers leave it empty and the verb runs against CWD.
var defaultWorktreeRoot string

// SetDefaultWorktreeRoot configures the server-wide fallback worktree
// root the verb uses when StoryRequestReviewRequest.WorktreeRoot is
// empty. Tests can set + clear between cases.
func SetDefaultWorktreeRoot(p string) { defaultWorktreeRoot = p }

// recentLedgerLimit caps how much of the story's ledger the gate skill
// sees on stdin. Five rows is plenty for a reviewer to spot recent
// context without bloating the prompt — the full history stays one
// `ledger_list` call away if the reviewer wants it.
const recentLedgerLimit = 5

// Ledger kinds the request_review verb writes. Operators can append
// other kinds via ledger_append; these are the canonical signals the
// substrate itself emits.
const (
	KindReviewAccept     = "review_accept"
	KindReviewReject     = "review_reject"
	KindStatusTransition = "status_transition"
)

func init() {
	Register(&Verb{
		Name: "story_request_review",
		Description: "Run the workflow gate skill for the story's current → next transition. " +
			"On accept the story status flips and a ledger row is written; on reject the ledger captures the rejection notes.",
		Invoke: invokeStoryRequestReview,
	})
}

func invokeStoryRequestReview(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("story_request_review: document store not configured")
	}
	if ledgerStore == nil {
		return nil, fmt.Errorf("story_request_review: ledger store not configured")
	}
	if gateDispatcher == nil {
		return nil, fmt.Errorf("story_request_review: gate dispatcher not configured")
	}

	var req StoryRequestReviewRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("story_request_review: %w: %v", ErrBadRequest, err)
		}
	}
	storyID := strings.TrimSpace(req.StoryID)
	if storyID == "" {
		return nil, fmt.Errorf("story_request_review: %w: story_id required", ErrBadRequest)
	}

	story, body, err := documentStore.GetByIDWithLatestBody(ctx, storyID)
	if err != nil {
		return nil, mapStoreError(err, "story_request_review")
	}
	if story.Type != document.TypeStory {
		return nil, fmt.Errorf("story_request_review: %w: id=%s is type=%s, expected story", ErrBadRequest, storyID, story.Type)
	}

	storyType := strings.TrimSpace(story.Category)
	if storyType == "" {
		return nil, fmt.Errorf("story_request_review: story %s has no category — workflow lookup needs a story_type", storyID)
	}

	cfg, err := loadProjectConfig(ctx, story.WorkspaceID, story.ProjectID)
	if err != nil {
		return nil, err
	}
	skillPath, ok := cfg.StoryTypes[storyType]
	if !ok || strings.TrimSpace(skillPath.WorkflowSkill) == "" {
		return nil, fmt.Errorf("story_request_review: project-config has no workflow_skill for story_type=%q", storyType)
	}

	worktreeRoot := strings.TrimSpace(req.WorktreeRoot)
	if worktreeRoot == "" {
		worktreeRoot = defaultWorktreeRoot
	}
	wfBytes, err := skillReader(worktreeRoot, skillPath.WorkflowSkill)
	if err != nil {
		return nil, fmt.Errorf("story_request_review: read workflow skill %q: %w", skillPath.WorkflowSkill, err)
	}
	wf, err := workflow.Parse(wfBytes)
	if err != nil {
		return nil, fmt.Errorf("story_request_review: parse workflow %q: %w", skillPath.WorkflowSkill, err)
	}

	transition, gateSkill, isDynamic, err := pickTransition(wf, story.Status)
	if err != nil {
		return nil, err
	}

	entries, err := recentLedger(ctx, storyID)
	if err != nil {
		return nil, fmt.Errorf("story_request_review: read recent ledger: %w", err)
	}

	actor := callerUserID(ctx)
	if actor == "" {
		return nil, fmt.Errorf("story_request_review: %w: authenticated caller required (reviewer-key mint needs a user_id)", ErrUnauthorized)
	}
	// agent_name must be unique per (user, project) because of the
	// api_keys_project_agent unique index (migration 0002). Story id +
	// epoch nanos is enough — a single executor never lands two
	// simultaneous reviews of the same story, and the nanos rule out
	// successive same-story retries colliding.
	agentName := fmt.Sprintf("review:%s:%s:%d", gateSkill, storyID, time.Now().UnixNano())
	minted, err := MintReviewerKeyForStory(ctx, MintReviewerKeyInput{
		StoryID:   storyID,
		UserID:    actor,
		ProjectID: story.ProjectID,
		AgentName: agentName,
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = RevokeReviewerKeyForStory(ctx, storyID, minted.KeyID, actor)
	}()

	gateOut, err := gateDispatcher.Dispatch(ctx, GateInput{
		SkillName:    gateSkill,
		StoryID:      storyID,
		StoryBody:    body,
		StoryStatus:  story.Status,
		NextStatus:   transition.To,
		Dynamic:      isDynamic,
		RecentLedger: entries,
		ReviewerKey:  minted.APIKey,
		WorktreeRoot: worktreeRoot,
	})
	if err != nil {
		return nil, fmt.Errorf("story_request_review: gate dispatch: %w", err)
	}

	resp, err := applyGateDecision(ctx, story, transition, isDynamic, gateOut, wf)
	if err != nil {
		return nil, err
	}
	return json.Marshal(resp)
}

// pickTransition is the verb's transition-resolution rule:
//
//   - If any outgoing edge from the current state is `dynamic: true`,
//     the workflow itself is the gate skill and isDynamic=true. The
//     dispatched skill returns the next_status, which must reference a
//     declared outgoing edge (verified by FindTransition on apply).
//   - Otherwise the verb picks the single outgoing edge with a non-empty
//     reviewer_skill. Multiple gated edges from the same state are an
//     error — the v5 contract calls for the workflow to disambiguate by
//     using `dynamic: true` in that case.
//
// Returns the chosen transition, the gate skill name (workflow name when
// dynamic, transition.ReviewerSkill otherwise), and whether the run is
// dynamic.
func pickTransition(wf *workflow.Workflow, currentStatus string) (workflow.Transition, string, bool, error) {
	outgoing := wf.TransitionsFrom(currentStatus)
	if len(outgoing) == 0 {
		return workflow.Transition{}, "", false, fmt.Errorf("story_request_review: no outgoing transitions from status %q", currentStatus)
	}
	for _, t := range outgoing {
		if t.Dynamic {
			return t, wf.Name, true, nil
		}
	}
	var gated []workflow.Transition
	for _, t := range outgoing {
		if strings.TrimSpace(t.ReviewerSkill) != "" {
			gated = append(gated, t)
		}
	}
	if len(gated) == 0 {
		return workflow.Transition{}, "", false, fmt.Errorf("story_request_review: no gated transition from status %q (every outgoing edge has empty reviewer_skill — use the body-only patch path)", currentStatus)
	}
	if len(gated) > 1 {
		return workflow.Transition{}, "", false, fmt.Errorf("story_request_review: multiple gated transitions from status %q — workflow must mark the choice as dynamic", currentStatus)
	}
	return gated[0], gated[0].ReviewerSkill, false, nil
}

// applyGateDecision writes the ledger row and (on accept) flips the
// story status under a reviewer-role ctx so the role gate from
// sty_25d5b21e passes.
func applyGateDecision(ctx context.Context, story document.Document, transition workflow.Transition, isDynamic bool, gateOut GateOutput, wf *workflow.Workflow) (StoryRequestReviewResponse, error) {
	resp := StoryRequestReviewResponse{Decision: gateOut.Decision, Notes: gateOut.Notes}

	if gateOut.Decision == GateDecisionReject {
		if err := writeReviewLedger(ctx, story.ID, KindReviewReject, story.Status, "", transition, gateOut); err != nil {
			return resp, err
		}
		return resp, nil
	}

	newStatus := transition.To
	if isDynamic {
		chosen := strings.TrimSpace(gateOut.NextStatus)
		if chosen == "" {
			return resp, fmt.Errorf("story_request_review: dynamic gate returned empty next_status")
		}
		if _, ok := wf.FindTransition(story.Status, chosen); !ok {
			return resp, fmt.Errorf("story_request_review: dynamic gate returned undeclared transition %s → %s", story.Status, chosen)
		}
		newStatus = chosen
	}

	patchCtx := auth.WithAPIKeyRole(ctx, auth.APIKeyRoleReviewer)
	patchedDoc, err := documentStore.UpdateStory(patchCtx, story.ID, document.UpdateStoryPatch{
		Status:    &newStatus,
		CreatedBy: callerUserID(ctx),
	}, time.Now().UTC())
	if err != nil {
		return resp, mapStoreError(err, "story_request_review")
	}

	if err := writeReviewLedger(ctx, story.ID, KindReviewAccept, story.Status, newStatus, transition, gateOut); err != nil {
		return resp, err
	}
	// Mirror the status transition as a separate ledger event so portal
	// timelines + filters can pivot on `status_transition` without
	// having to decode the accept payload.
	if err := writeStatusTransitionLedger(ctx, story.ID, story.Status, newStatus); err != nil {
		return resp, err
	}

	resp.NewStatus = patchedDoc.Status
	return resp, nil
}

func writeReviewLedger(ctx context.Context, storyID, kind, fromStatus, toStatus string, transition workflow.Transition, out GateOutput) error {
	payload, _ := json.Marshal(map[string]any{
		"from_status":     fromStatus,
		"to_status":       toStatus,
		"reviewer_skill":  transition.ReviewerSkill,
		"transition_to":   transition.To,
		"transition_from": transition.From,
		"dynamic":         transition.Dynamic,
		"notes":           out.Notes,
	})
	_, err := ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: storyID,
		Kind:    kind,
		Actor:   "system:request_review",
		Body:    out.Notes,
		Payload: payload,
	}, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("story_request_review: ledger append %s: %w", kind, err)
	}
	return nil
}

func writeStatusTransitionLedger(ctx context.Context, storyID, fromStatus, toStatus string) error {
	payload, _ := json.Marshal(map[string]any{
		"from_status": fromStatus,
		"to_status":   toStatus,
	})
	_, err := ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: storyID,
		Kind:    KindStatusTransition,
		Actor:   "system:request_review",
		Body:    fmt.Sprintf("%s → %s", fromStatus, toStatus),
		Payload: payload,
	}, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("story_request_review: ledger append status_transition: %w", err)
	}
	return nil
}

func loadProjectConfig(ctx context.Context, workspaceID, projectID string) (projectConfig, error) {
	res, err := documentStore.Get(ctx, document.Key{
		Scope:       document.ScopeProject,
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Name:        "project-config",
	}, document.GetOptions{})
	if err != nil {
		return projectConfig{}, fmt.Errorf("story_request_review: load project-config: %w", err)
	}
	if len(res.Versions) == 0 {
		return projectConfig{}, fmt.Errorf("story_request_review: project-config has no active version")
	}
	return parseProjectConfig(res.Versions[0].Body)
}

// parseProjectConfig turns a project-config document body into the typed
// config. The body is YAML inside a markdown ```yaml fence (so humans can
// annotate around it), so we extract the fenced block before unmarshalling
// — the same fence-vs-raw class of bug the workflow loader already handles
// (sty_8da63e77). A body with no fence is treated as raw YAML so legacy
// fence-less configs still parse.
func parseProjectConfig(body string) (projectConfig, error) {
	raw := []byte(body)
	if block, err := workflow.ExtractYAMLBlock(raw); err == nil {
		raw = block
	}
	var cfg projectConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return projectConfig{}, fmt.Errorf("story_request_review: parse project-config yaml: %w", err)
	}
	if len(cfg.StoryTypes) == 0 {
		return projectConfig{}, fmt.Errorf("story_request_review: project-config has empty story_types block")
	}
	return cfg, nil
}

func recentLedger(ctx context.Context, storyID string) ([]ledger.Entry, error) {
	entries, err := ledgerStore.ListByStory(ctx, storyID, "")
	if err != nil {
		return nil, err
	}
	if len(entries) <= recentLedgerLimit {
		return entries, nil
	}
	return entries[len(entries)-recentLedgerLimit:], nil
}
