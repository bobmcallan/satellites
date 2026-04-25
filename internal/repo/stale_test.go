package repo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/task"
)

// stubResolver answers HeadSHA from a per-remote map; missing entries
// trigger the error path.
type stubResolver struct {
	heads  map[string]string
	errors map[string]error
}

func (s *stubResolver) HeadSHA(ctx context.Context, gitRemote, branch string) (string, error) {
	if err, ok := s.errors[gitRemote]; ok {
		return "", err
	}
	if h, ok := s.heads[gitRemote]; ok {
		return h, nil
	}
	return "", errors.New("no head configured for " + gitRemote)
}

func newStaleFixture(t *testing.T) StaleCheckDeps {
	t.Helper()
	repos := NewMemoryStore()
	tasks := task.NewMemoryStore()
	led := ledger.NewMemoryStore()
	return StaleCheckDeps{
		Repos:  repos,
		Tasks:  tasks,
		Ledger: led,
	}
}

func TestRunStaleCheck_EnqueuesOnDrift(t *testing.T) {
	t.Parallel()
	deps := newStaleFixture(t)
	now := time.Date(2026, 4, 25, 3, 0, 0, 0, time.UTC)
	deps.Now = func() time.Time { return now }

	driftRepo, err := deps.Repos.Create(context.Background(), Repo{
		WorkspaceID:   "ws_1",
		ProjectID:     "proj_a",
		GitRemote:     "git@host:drift.git",
		DefaultBranch: "main",
		HeadSHA:       "old-sha",
	}, now)
	if err != nil {
		t.Fatalf("seed drift repo: %v", err)
	}
	stableRepo, err := deps.Repos.Create(context.Background(), Repo{
		WorkspaceID:   "ws_1",
		ProjectID:     "proj_b",
		GitRemote:     "git@host:stable.git",
		DefaultBranch: "main",
		HeadSHA:       "stable-sha",
	}, now)
	if err != nil {
		t.Fatalf("seed stable repo: %v", err)
	}

	deps.Resolver = &stubResolver{heads: map[string]string{
		driftRepo.GitRemote:  "new-sha",
		stableRepo.GitRemote: "stable-sha",
	}}

	got, err := RunStaleCheck(context.Background(), deps)
	if err != nil {
		t.Fatalf("RunStaleCheck: %v", err)
	}
	if got.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2", got.Scanned)
	}
	if got.EnqueuedReindex != 1 {
		t.Errorf("EnqueuedReindex = %d, want 1", got.EnqueuedReindex)
	}
	if got.Errors != 0 {
		t.Errorf("Errors = %d, want 0", got.Errors)
	}

	tasks, _ := deps.Tasks.List(context.Background(), task.ListOptions{}, nil)
	if len(tasks) != 1 {
		t.Fatalf("tasks queued = %d, want 1", len(tasks))
	}
	if !strings.Contains(string(tasks[0].Payload), driftRepo.ID) {
		t.Errorf("queued task payload missing drift repo id: %s", string(tasks[0].Payload))
	}

	rows, _ := deps.Ledger.List(context.Background(), "", ledger.ListOptions{}, nil)
	if !hasReindexTag(rows, tagStaleCheckComplete) {
		t.Errorf("missing %s row; tags=%v", tagStaleCheckComplete, flattenReindexTags(rows))
	}
}

func TestRunStaleCheck_HandlesResolverErrors(t *testing.T) {
	t.Parallel()
	deps := newStaleFixture(t)
	now := time.Date(2026, 4, 25, 3, 0, 0, 0, time.UTC)
	deps.Now = func() time.Time { return now }

	good, err := deps.Repos.Create(context.Background(), Repo{
		WorkspaceID:   "ws_1",
		ProjectID:     "proj_a",
		GitRemote:     "git@host:good.git",
		DefaultBranch: "main",
		HeadSHA:       "old",
	}, now)
	if err != nil {
		t.Fatalf("seed good: %v", err)
	}
	bad, err := deps.Repos.Create(context.Background(), Repo{
		WorkspaceID:   "ws_1",
		ProjectID:     "proj_b",
		GitRemote:     "git@host:bad.git",
		DefaultBranch: "main",
		HeadSHA:       "x",
	}, now)
	if err != nil {
		t.Fatalf("seed bad: %v", err)
	}

	deps.Resolver = &stubResolver{
		heads: map[string]string{good.GitRemote: "new"},
		errors: map[string]error{
			bad.GitRemote: errors.New("git ls-remote: connection refused"),
		},
	}

	got, err := RunStaleCheck(context.Background(), deps)
	if err != nil {
		t.Fatalf("RunStaleCheck: %v", err)
	}
	if got.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2", got.Scanned)
	}
	if got.Errors != 1 {
		t.Errorf("Errors = %d, want 1", got.Errors)
	}
	if got.EnqueuedReindex != 1 {
		t.Errorf("EnqueuedReindex = %d, want 1 (good drifted, bad errored)", got.EnqueuedReindex)
	}

	rows, _ := deps.Ledger.List(context.Background(), "", ledger.ListOptions{}, nil)
	if !hasReindexTag(rows, tagStaleCheckError) {
		t.Errorf("missing %s row for resolver-failed repo", tagStaleCheckError)
	}
	if !hasReindexTag(rows, tagStaleCheckComplete) {
		t.Errorf("missing %s row", tagStaleCheckComplete)
	}
}
