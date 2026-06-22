// Checkpoint gate verdict evidence (epic:graduated-workflow,
// gate-verdict-evidence). The deterministic checkpoint gates — techdebt
// review, surface check, workflow check — append their CLEAN/BLOCKED verdict
// to the engaged story's ledger AT VERDICT TIME, written by this code and
// never by the agent: the row is the answer to "was the gate run?" and the
// done reviewer's checkpoint-compliance evidence. Reuses the `evidence ci`
// write surface (local RecordEvidence + a ci_result ledger row) rather than
// inventing a new one. With no engaged story the gate is print-only —
// unchanged behaviour outside a story.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workstate"
)

// Gate verdict discriminators — the two words the gates print.
const (
	gateVerdictClean   = "CLEAN"
	gateVerdictBlocked = "BLOCKED"
)

// recordGateVerdict appends a checkpoint gate's verdict to the engaged
// story's ledger. Best-effort end to end: no engagement, an unopenable
// store, or a failed append all leave the gate's own verdict (its exit
// code) untouched — evidence must never change a gate's decision.
func recordGateVerdict(ctx context.Context, configPath, userArg, gate, verdict string, blockingFindings int, duration time.Duration, out io.Writer) {
	story := engagedStoryForSession(configPath, resolveSession(""))
	if story == "" {
		return // print-only outside an engagement
	}
	appendLedger := func(ctx context.Context, req json.RawMessage) error {
		_, err := dispatchVerb(ctx, "ledger_append", req, configPath, userArg)
		return err
	}
	recordGateVerdictTo(ctx, resolveStateDB(configPath), story, gate, verdict, blockingFindings, duration, out, appendLedger)
}

// engagedStoryForSession resolves the story this session is engaged on, or
// "" when there is none (or the store is unreadable — fail open to
// print-only, never block the gate).
func engagedStoryForSession(configPath, session string) string {
	store, err := workstate.Open(resolveStateDB(configPath))
	if err != nil {
		return ""
	}
	defer store.Close()
	engs, err := store.ListCurrent()
	if err != nil {
		return ""
	}
	for _, e := range engs {
		if e.Session == session {
			return e.Story
		}
	}
	return ""
}

// recordGateVerdictTo is the injectable core: one
// local evidence row + one ci_result ledger row carrying
// {gate, verdict, blocking_findings, duration_ms}. Pure enough for tests to
// pin the row shape without a server.
func recordGateVerdictTo(ctx context.Context, stateDB, story, gate, verdict string, blockingFindings int, duration time.Duration, out io.Writer, appendLedger func(context.Context, json.RawMessage) error) {
	if strings.TrimSpace(story) == "" {
		return
	}
	if store, err := workstate.Open(stateDB); err == nil {
		_, _ = store.RecordEvidence(workstate.Evidence{
			Story:    story,
			Kind:     workstate.EvidenceGate,
			Label:    gate,
			Decision: verdict,
			Notes:    fmt.Sprintf("blocking_findings=%d duration_ms=%d", blockingFindings, duration.Milliseconds()),
			TS:       time.Now(),
		})
		store.Close()
	}
	payload, _ := json.Marshal(map[string]any{
		"gate":              gate,
		"verdict":           verdict,
		"blocking_findings": blockingFindings,
		"duration_ms":       duration.Milliseconds(),
	})
	req, err := json.Marshal(verb.LedgerAppendRequest{
		StoryID: story,
		Kind:    "ci_result",
		Body:    fmt.Sprintf("gate %s: %s", gate, verdict),
		Payload: payload,
	})
	if err != nil || appendLedger == nil {
		return
	}
	if lErr := appendLedger(ctx, req); lErr != nil {
		fmt.Fprintf(out, "warn: gate verdict row for %s not written (gate verdict unaffected): %v\n", story, lErr)
		return
	}
	fmt.Fprintf(out, "gate verdict recorded on %s (%s: %s)\n", story, gate, verdict)
}
