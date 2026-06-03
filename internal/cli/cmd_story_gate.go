// `satellites story gate <story-id>...` — the deploy-path enforcement check
// (sty_1ad84429, epic gated-deploy). Given the story ids a deploy's commit
// range references, it REFUSES (non-zero exit) unless every named story has
// reached `done` — the terminal gated status whose sole writer is a
// reviewer-key-enacted status_transition row (internal/verb/ledger.go).
//
// Status is the projection of a reviewer-only ledger row: the role gate
// refuses status_transition from an executor key, so a `done` status is
// evidence the executor could not have forged. Reading it therefore satisfies
// "enforcement does NOT depend on executor self-assertion" (AC#4) without the
// gate re-deriving the ledger — `done` already implies an accepted, enacted
// done-review.
//
// It composes the existing document_get verb — no new MCP verb
// (document:project/no-new-mcp-verbs). The name is deliberately `story gate`,
// NOT any `deploy` form: `satellites deploy` is the skill-sync pull, a
// different surface the spike warned never to conflate.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// gatedDeployStatus is the status a story must hold for a deploy of its code
// to be legitimate. `done` is the terminal state of the configured workflow,
// reached only via an accepted satellites-story-done-review (spike §3).
const gatedDeployStatus = "done"

// gateStory is the minimal projection the deploy gate reads per story.
type gateStory struct {
	ID     string
	Type   string
	Status string
	// Found is false when document_get returned no such story (a ref that
	// names nothing) — treated as a refusal, not an allow.
	Found bool
}

// evaluateDeployGate decides whether a deploy carrying the given stories is
// legitimate, and returns the legible report AC#3 requires. A deploy is
// allowed only when every entry is a story at gatedDeployStatus. Pure (no
// I/O) so the decision is unit-tested directly.
func evaluateDeployGate(stories []gateStory) (ok bool, report string) {
	var b strings.Builder
	blocked := 0
	for _, s := range stories {
		switch {
		case !s.Found:
			fmt.Fprintf(&b, "  ✗ %s — no such story (ref names nothing)\n", s.ID)
			blocked++
		case s.Type != "story":
			fmt.Fprintf(&b, "  ✗ %s — not a story (type=%s)\n", s.ID, s.Type)
			blocked++
		case s.Status != gatedDeployStatus:
			fmt.Fprintf(&b, "  ✗ %s — status=%s, not %s (the done gate has not accepted this story's code)\n", s.ID, s.Status, gatedDeployStatus)
			blocked++
		default:
			fmt.Fprintf(&b, "  ✓ %s — %s\n", s.ID, s.Status)
		}
	}
	if blocked == 0 {
		return true, fmt.Sprintf("deploy-gate: all %d story(ies) gated to %s — deploy allowed.\n%s", len(stories), gatedDeployStatus, b.String())
	}
	footer := fmt.Sprintf(
		"\ndeploy-gate: REFUSED — %d of %d story(ies) have not passed the done gate.\n"+
			"A story's code may roll out only after `satellites story review <id>` accepts its\n"+
			"in_progress→done transition under a reviewer key. Gate the story(ies) marked ✗ above,\n"+
			"then re-trigger the deploy.\n",
		blocked, len(stories))
	return false, "deploy-gate: checking story status\n" + b.String() + footer
}

// gateFetchFn resolves one story's gate projection. Abstracted so the command
// wires it to a document_get dispatch while tests inject a fake.
type gateFetchFn func(ctx context.Context, id string) (gateStory, error)

// runStoryGate fetches each story and prints the gate report. It returns a
// non-nil error (rendered by cobra as a non-zero exit) when the deploy is
// refused, so CI's `deploy` job — which `needs` this gate — does not roll out.
func runStoryGate(ctx context.Context, ids []string, fetch gateFetchFn, out io.Writer) error {
	if len(ids) == 0 {
		return fmt.Errorf("story gate: at least one story id required")
	}
	stories := make([]gateStory, 0, len(ids))
	for _, id := range ids {
		s, err := fetch(ctx, id)
		if err != nil {
			return fmt.Errorf("resolve story %s: %w", id, err)
		}
		stories = append(stories, s)
	}
	ok, report := evaluateDeployGate(stories)
	fmt.Fprint(out, report)
	if !ok {
		return fmt.Errorf("deploy refused: %d story(ies) not gated to %s", countBlocked(stories), gatedDeployStatus)
	}
	return nil
}

// countBlocked reports how many stories fail the gate — used only for the
// one-line error summary; the per-story detail is in the printed report.
func countBlocked(stories []gateStory) int {
	n := 0
	for _, s := range stories {
		if !s.Found || s.Type != "story" || s.Status != gatedDeployStatus {
			n++
		}
	}
	return n
}

func newStoryGateCmd(configArg, userArg *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gate <story-id>...",
		Short: "Refuse a deploy unless every named story has passed the done gate (deploy-path enforcement)",
		Long: `gate is the deploy-path enforcement check (epic gated-deploy).

Given the story ids a deploy's commit range references, it refuses (non-zero
exit) unless every named story has reached ` + "`done`" + ` — the status whose sole
writer is a reviewer-key-enacted status_transition row, so it cannot be set by
an executor. CI runs this upstream of the Fly rollout; a refusal blocks it.

It reads each story's status via document_get (no new server verb). In CI the
api-key is injected via $SATELLITES_API_KEY and the server_url comes from the
repo-committed .satellites/satellites.toml.`,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			ids := make([]string, 0, len(args))
			for _, a := range args {
				if t := strings.TrimSpace(a); t != "" {
					ids = append(ids, t)
				}
			}
			fetch := func(ctx context.Context, id string) (gateStory, error) {
				return fetchGateStory(ctx, id, *configArg, *userArg)
			}
			return runStoryGate(ctx, ids, fetch, cmd.OutOrStdout())
		},
	}
	return cmd
}

// fetchGateStory resolves one story's gate projection via document_get. A
// "not found" reply is reported as Found:false (a refusal), not an error, so a
// commit referencing a dead id blocks rather than silently passing.
func fetchGateStory(ctx context.Context, id, configPath, userArg string) (gateStory, error) {
	req, err := json.Marshal(verb.DocumentGetRequest{ID: id})
	if err != nil {
		return gateStory{}, err
	}
	raw, err := dispatchVerb(ctx, "document_get", req, configPath, userArg)
	if err != nil {
		// A not-found story surfaces as a dispatch error; treat it as an
		// unfound ref (refusal) rather than aborting the whole gate run.
		if isNotFound(err) {
			return gateStory{ID: id, Found: false}, nil
		}
		return gateStory{}, err
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return gateStory{}, fmt.Errorf("decode story: %w", err)
	}
	if strings.TrimSpace(resp.Document.ID) == "" {
		return gateStory{ID: id, Found: false}, nil
	}
	return gateStory{
		ID:     resp.Document.ID,
		Type:   resp.Document.Type,
		Status: resp.Document.Status,
		Found:  true,
	}, nil
}

// isNotFound reports whether a dispatch error is a missing-row reply rather
// than a transport/auth failure. A missing story is a gate refusal; a
// transport failure must abort so the gate never passes on a partial read.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "no such")
}
