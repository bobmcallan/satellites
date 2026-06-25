// `satellites story estimate` / `satellites story actual` — record a story's
// plan ESTIMATE and its ACTUAL token usage on the story itself (sty_9643a847).
//
//	satellites story estimate <sty> --time 30m --tokens 40000 --basis "2 files, gate loop x3"
//	satellites story actual   <sty> --tokens 38000 [--time 28m]
//
// The close-out panel (processtrace.CloseOut) compares estimate-vs-actual but
// had no producer: nothing ever recorded an estimate, and there is no automatic
// token feed, so every ESTIMATE cell rendered "—" and actual tokens never
// populated. These verbs are the producer — thin MECHANISM over the existing
// document_upsert verb that writes the values as KV tags the close-out reader
// sources (estimate-minutes / estimate-tokens / estimate-basis, actual-tokens /
// actual-minutes). The plan gate (satellites-intent-plan-review) rejects a story
// with no recorded estimate; the done gate (satellites-story-done-review)
// rejects one with no recorded actual tokens — the executor self-reports, as
// there is no metrics feed to wire.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/kvtag"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// Close-out KV tag keys. The processtrace close-out reader sources estimate and
// actual values from these keys — they are the single recorded source.
const (
	tagEstimateMinutes = "estimate-minutes"
	tagEstimateTokens  = "estimate-tokens"
	tagEstimateBasis   = "estimate-basis"
	tagActualMinutes   = "actual-minutes"
	tagActualTokens    = "actual-tokens"
)

// storyEstimateOpts is the flag surface for `story estimate`.
type storyEstimateOpts struct {
	Time   string
	Tokens int
	Basis  string
}

// storyActualOpts is the flag surface for `story actual`.
type storyActualOpts struct {
	Time   string
	Tokens int
}

func newStoryEstimateCmd(configArg, userArg *string) *cobra.Command {
	var o storyEstimateOpts
	cmd := &cobra.Command{
		Use:   "estimate <story-id> (--time <dur> | --tokens <n>) [--basis <text>]",
		Short: "Record a story's plan estimate (time/tokens/basis) for the close-out",
		Long: `estimate records the plan-stage projection the close-out panel compares against
actuals. It writes the values as KV tags on the story (estimate-minutes,
estimate-tokens, estimate-basis) via the document_upsert patch path — no second
write surface. Run it at planning, before requesting satellites-intent-plan-review,
which rejects a story with no recorded estimate.

At least one of --time or --tokens is required. --time accepts a Go duration
(e.g. 30m, 1h30m); it is stored rounded to whole minutes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runStoryEstimate(ctx, cmd.OutOrStdout(), *configArg, *userArg, strings.TrimSpace(args[0]), o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Time, "time", "", "Estimated time as a Go duration (e.g. 30m, 1h30m)")
	f.IntVar(&o.Tokens, "tokens", 0, "Estimated token budget (raw count, e.g. 40000)")
	f.StringVar(&o.Basis, "basis", "", "Short note on how the estimate was derived")
	return cmd
}

func newStoryActualCmd(configArg, userArg *string) *cobra.Command {
	var o storyActualOpts
	cmd := &cobra.Command{
		Use:   "actual <story-id> --tokens <n> [--time <dur>]",
		Short: "Record a story's actual token usage (and optional time) for the close-out",
		Long: `actual records what the story actually consumed, for the close-out's ACTUAL
column. There is no automatic token feed, so the executor self-reports its token
usage here; the done gate (satellites-story-done-review) rejects a story with no
recorded actual tokens. Values are stored as KV tags (actual-tokens,
actual-minutes) via document_upsert.

--tokens is required. --time is optional: elapsed minutes are derived
automatically from the ledger spine, so pass --time only to override that.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runStoryActual(ctx, cmd.OutOrStdout(), *configArg, *userArg, strings.TrimSpace(args[0]), o)
		},
	}
	f := cmd.Flags()
	f.IntVar(&o.Tokens, "tokens", 0, "Actual tokens consumed (raw count, e.g. 38000)")
	f.StringVar(&o.Time, "time", "", "Override actual time as a Go duration (default: derived from the ledger)")
	return cmd
}

// durationMinutes parses a Go duration string into whole minutes (rounded).
func durationMinutes(s string) (int, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 0, err
	}
	return int(d.Round(time.Minute) / time.Minute), nil
}

func runStoryEstimate(ctx context.Context, out io.Writer, configPath, userArg, storyID string, o storyEstimateOpts) error {
	if strings.TrimSpace(o.Time) == "" && o.Tokens <= 0 {
		return fmt.Errorf("story estimate: provide at least one of --time or --tokens")
	}
	sets := map[string]string{}
	if strings.TrimSpace(o.Time) != "" {
		mins, err := durationMinutes(o.Time)
		if err != nil {
			return fmt.Errorf("story estimate: parse --time: %w", err)
		}
		sets[tagEstimateMinutes] = fmt.Sprintf("%d", mins)
	}
	if o.Tokens > 0 {
		sets[tagEstimateTokens] = fmt.Sprintf("%d", o.Tokens)
	}
	if b := sanitizeTagValue(o.Basis); b != "" {
		sets[tagEstimateBasis] = b
	}
	return patchStoryCloseOutTags(ctx, out, configPath, userArg, storyID, sets, "estimate")
}

func runStoryActual(ctx context.Context, out io.Writer, configPath, userArg, storyID string, o storyActualOpts) error {
	if o.Tokens <= 0 {
		return fmt.Errorf("story actual: --tokens is required (the actual token count)")
	}
	sets := map[string]string{tagActualTokens: fmt.Sprintf("%d", o.Tokens)}
	if strings.TrimSpace(o.Time) != "" {
		mins, err := durationMinutes(o.Time)
		if err != nil {
			return fmt.Errorf("story actual: parse --time: %w", err)
		}
		sets[tagActualMinutes] = fmt.Sprintf("%d", mins)
	}
	return patchStoryCloseOutTags(ctx, out, configPath, userArg, storyID, sets, "actual")
}

// sanitizeTagValue collapses whitespace (tags are single-line strings) and trims
// the basis note so it round-trips cleanly as a KV tag value.
func sanitizeTagValue(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// patchStoryCloseOutTags resolves the story, replace-by-key sets each close-out
// tag onto its existing tag set, and writes the merged set back via
// document_upsert. Reading-then-merging preserves the story's other tags
// (workflow:, area:, …) — document_upsert replaces the whole tag field.
func patchStoryCloseOutTags(ctx context.Context, out io.Writer, configPath, userArg, storyID string, sets map[string]string, verbName string) error {
	getReq, _ := json.Marshal(verb.DocumentGetRequest{ID: storyID})
	raw, err := dispatchVerb(ctx, "document_get", getReq, configPath, userArg)
	if err != nil {
		return fmt.Errorf("story %s: resolve %s: %w", verbName, storyID, err)
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("story %s: decode %s: %w", verbName, storyID, err)
	}
	if resp.Document.Type != storyType {
		return fmt.Errorf("story %s: %s is type=%q, not a story", verbName, storyID, resp.Document.Type)
	}

	tags := mergeCloseOutTags(resp.Document.Tags, sets)
	upReq, _ := json.Marshal(verb.DocumentUpsertRequest{ID: storyID, Type: storyType, Tags: &tags})
	if _, err := dispatchVerb(ctx, "document_upsert", upReq, configPath, userArg); err != nil {
		return fmt.Errorf("story %s: write tags: %w", verbName, err)
	}

	rendered := make([]string, 0, len(sets))
	for _, k := range []string{tagEstimateMinutes, tagEstimateTokens, tagEstimateBasis, tagActualMinutes, tagActualTokens} {
		if v, ok := sets[k]; ok {
			rendered = append(rendered, k+":"+v)
		}
	}
	fmt.Fprintf(out, "%s %s  [%s]\n", storyID, verbName, strings.Join(rendered, ", "))
	return nil
}

// mergeCloseOutTags applies each close-out key/value onto the existing tag set
// with replace-by-key semantics (kvtag.Set), returning the full merged set.
// Pure, for tests.
func mergeCloseOutTags(existing []string, sets map[string]string) []string {
	tags := append([]string(nil), existing...)
	// Apply in a stable key order so the result is deterministic.
	for _, k := range []string{tagEstimateMinutes, tagEstimateTokens, tagEstimateBasis, tagActualMinutes, tagActualTokens} {
		if v, ok := sets[k]; ok {
			tags = kvtag.Set(tags, k, v)
		}
	}
	return tags
}
