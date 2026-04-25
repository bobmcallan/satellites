package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/ledger"
)

// tagCommit is the canonical kind tag for per-commit ledger rows. The
// portal's story-view repo-provenance panel reads rows carrying this
// tag (internal/portal/story_view.go:259).
const tagCommit = "kind:commit"

// storyRefPattern matches `story_<8hex>` and `#story_<8hex>`
// case-insensitively. Used by extractStoryRefs to find references in
// commit messages.
var storyRefPattern = regexp.MustCompile(`(?i)#?(story_[0-9a-f]{8})\b`)

// extractStoryRefs returns the canonical (lower-case) story IDs
// referenced in a commit message. Duplicates within the same message
// are collapsed; ordering follows first occurrence.
func extractStoryRefs(message string) []string {
	matches := storyRefPattern.FindAllStringSubmatch(message, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		id := strings.ToLower(m[1])
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// commitStructured is the JSON payload persisted on each kind:commit
// row. Field names match what the story-view consumer
// (internal/portal/story_view.go:274) reads.
type commitStructured struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author,omitempty"`
	URL     string `json:"url,omitempty"`
}

// emitCommitRows writes one kind:commit ledger row per (commit,
// story_id) pair found in the supplied commits. Idempotent on retry: a
// (sha, story_id) that already has a row is skipped via a tag-narrowed
// List probe. Failures to probe or append are non-fatal — the webhook
// path continues to the reindex enqueue regardless.
func emitCommitRows(ctx context.Context, store ledger.Store, repo Repo, commits []pushCommit, now time.Time) {
	if store == nil || len(commits) == 0 {
		return
	}
	for _, c := range commits {
		sha := strings.TrimSpace(c.ID)
		if sha == "" {
			continue
		}
		refs := extractStoryRefs(c.Message)
		if len(refs) == 0 {
			continue
		}
		subject := commitSubject(c.Message)
		author := c.Author.Name
		for _, storyID := range refs {
			if commitRowExists(ctx, store, repo.ProjectID, storyID, sha) {
				continue
			}
			payload, _ := json.Marshal(commitStructured{
				SHA:     sha,
				Subject: subject,
				Author:  author,
				URL:     c.URL,
			})
			storyRef := storyID
			_, _ = store.Append(ctx, ledger.LedgerEntry{
				WorkspaceID: repo.WorkspaceID,
				ProjectID:   repo.ProjectID,
				StoryID:     &storyRef,
				Type:        ledger.TypeDecision,
				Tags: []string{
					tagCommit,
					"repo_id:" + repo.ID,
					"sha:" + sha,
					"story_id:" + storyID,
				},
				Content:    fmt.Sprintf("commit %s linked to %s", shortSHA(sha), storyID),
				Structured: payload,
			}, now)
		}
	}
}

// commitRowExists probes the ledger for a prior kind:commit row for
// the (sha, story_id) pair. Tag matching on the store is OR-semantics,
// so we narrow with StoryID + sha tag and post-filter for kind:commit.
func commitRowExists(ctx context.Context, store ledger.Store, projectID, storyID, sha string) bool {
	rows, err := store.List(ctx, projectID, ledger.ListOptions{
		StoryID: storyID,
		Tags:    []string{"sha:" + sha},
		Limit:   ledger.MaxListLimit,
	}, nil)
	if err != nil {
		return false
	}
	for _, r := range rows {
		if hasTag(r.Tags, tagCommit) {
			return true
		}
	}
	return false
}

// commitSubject returns the first line of a commit message, trimmed.
func commitSubject(message string) string {
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		return strings.TrimSpace(message[:i])
	}
	return strings.TrimSpace(message)
}

// shortSHA returns the first 7 chars of a sha, or the full sha if
// shorter. Used for human-readable Content lines on emitted rows.
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}
