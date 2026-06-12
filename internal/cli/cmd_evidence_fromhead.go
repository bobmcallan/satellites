// `satellites evidence ci --from-head` — record the CI chain's outcomes for
// HEAD's story in one verb call (sty_26adfec7). Replaces the unversioned
// scripts/record-ci-evidence.sh so the checkpoint capability is self-contained:
// story id from the commit trailer, one `gh run list` conclusion per workflow
// (test/release/deploy), a ci_result row per concluded stage through the same
// runEvidenceCI path. A stage with no concluded run is skipped, so the call is
// idempotent and safe to re-run while the chain is still in flight.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

// ciStages is the recorded CI chain, in execution order. Each entry is both
// the GitHub workflow name and the evidence stage label.
var ciStages = []string{"test", "release", "deploy"}

// storyTrailerRe matches a story id in a commit message; the LAST match wins
// (the trailer convention puts it at the end of the subject or body).
var storyTrailerRe = regexp.MustCompile(`sty_[0-9a-f]+`)

// storyIDFromMessage pulls the story id out of a commit message — pure for
// tests. Empty when the message carries no sty_ trailer.
func storyIDFromMessage(msg string) string {
	m := storyTrailerRe.FindAllString(msg, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1]
}

// ciResultFromConclusion maps a gh run conclusion onto the closed result
// vocabulary — pure for tests. Anything but success records as failure.
func ciResultFromConclusion(conclusion string) string {
	if strings.TrimSpace(conclusion) == "success" {
		return "success"
	}
	return "failure"
}

// runEvidenceCIFromHead resolves the sha (HEAD unless base.Ref pins one) and
// story (arg unless the commit trailer carries it), reads each workflow's
// conclusion via gh, and records every concluded stage.
func runEvidenceCIFromHead(ctx context.Context, out io.Writer, story string, base evidenceCIOpts, appendLedger func(context.Context, json.RawMessage) error) error {
	sha := strings.TrimSpace(base.Ref)
	if sha == "" {
		b, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
		if err != nil {
			return fmt.Errorf("evidence ci --from-head: git rev-parse HEAD: %w", err)
		}
		sha = strings.TrimSpace(string(b))
	}
	if strings.TrimSpace(story) == "" {
		b, err := exec.CommandContext(ctx, "git", "log", "-1", "--format=%B", sha).Output()
		if err != nil {
			return fmt.Errorf("evidence ci --from-head: read commit message %s: %w", sha, err)
		}
		story = storyIDFromMessage(string(b))
		if story == "" {
			return fmt.Errorf("evidence ci --from-head: no sty_ trailer on %.8s — pass the story id", sha)
		}
	}
	fmt.Fprintf(out, "recording CI evidence for %s @ %.8s\n", story, sha)
	for _, stage := range ciStages {
		conclRaw, err := exec.CommandContext(ctx, "gh", "run", "list", "--commit", sha,
			"--workflow", stage, "--json", "conclusion", "--jq", ".[0].conclusion // empty").Output()
		conclusion := strings.TrimSpace(string(conclRaw))
		if err != nil || conclusion == "" {
			fmt.Fprintf(out, "  %s: no concluded run for %.8s — skipped\n", stage, sha)
			continue
		}
		result := ciResultFromConclusion(conclusion)
		opts := base
		opts.Story, opts.Stage, opts.Result, opts.Ref = story, stage, result, sha
		opts.Notes = fmt.Sprintf("gh %s conclusion=%s", stage, conclusion)
		if err := runEvidenceCI(ctx, io.Discard, opts, appendLedger); err != nil {
			return err
		}
		fmt.Fprintf(out, "  %s: %s (gh: %s)\n", stage, result, conclusion)
	}
	fmt.Fprintf(out, "done — see: satellites evidence show %s\n", story)
	return nil
}
