// Background QA audit (sty_8ba432e7, epic:qa-observability). Where Reconcile
// renders one story's declared-vs-actual process for the QA view, Audit walks
// the same durable ledger trail and flags LOOP ANOMALIES the spike (sty_ff49fec1
// AC3) catalogued from real incidents — ungated deploy, ship-then-no-gate, a
// spurious enact-failure reject, status drift, unengaged work, a reject with no
// follow-up.
//
// It composes with Reconcile rather than duplicating it: it reuses the same
// LedgerEntry projection and is a pure function over its inputs, so it unit-tests
// against synthetic fixtures with no DB. Observability only — it never writes a
// ledger row or patches a status; findings surface to the operator out of band
// (the CLI), never the executor's turn.

package processtrace

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// kindCIResult is the CI-outcome row order:8 (sty_7d2e9847) adds to the ledger;
// it is what makes ungated-deploy newly detectable.
const kindCIResult = "ci_result"

// Anomaly codes — stable strings so findings are matchable by an operator or a
// downstream panel, not free text.
const (
	AnomalyStatusDrift      = "status-drift"
	AnomalyShipThenNoGate   = "ship-then-no-gate"
	AnomalySpuriousReject   = "spurious-enact-failure-reject"
	AnomalyRejectNoFollowUp = "reject-with-no-follow-up"
	AnomalyUngatedDeploy    = "ungated-deploy"
	AnomalyUnengagedWork    = "unengaged-work"
)

// Severities.
const (
	SeverityWarn  = "warn"
	SeverityError = "error"
)

// Finding is one detected anomaly, addressable by story.
type Finding struct {
	StoryID  string `json:"story_id"`
	Anomaly  string `json:"anomaly"`
	Severity string `json:"severity"`
	Detail   string `json:"detail"`
}

// StoryAudit is the per-story input: its current status (= documents.status) and
// its durable ledger trail (any order). StoryType is carried for the finding's
// context but not required by any check.
type StoryAudit struct {
	StoryID       string
	StoryType     string
	CurrentStatus string
	Entries       []LedgerEntry
	// Terminal / Editable classify a status against the story's GOVERNING workflow
	// when the caller resolved one (sty_9f97ff5c) — so the detectors are
	// workflow-derived, not status-coded. nil falls back to the canonical
	// lifecycle ({done,cancelled} terminal; started-non-terminal editable), which
	// preserves behaviour for an unresolved/legacy story.
	Terminal func(string) bool
	Editable func(string) bool
}

// terminal reports whether `status` is a terminal state — the caller's
// workflow-derived classifier when set, else the canonical {done, cancelled}.
func (s StoryAudit) terminal(status string) bool {
	if s.Terminal != nil {
		return s.Terminal(status)
	}
	st := strings.TrimSpace(status)
	return st == "done" || st == "cancelled"
}

// editable reports whether `status` is a started, non-terminal working state —
// the caller's workflow-derived classifier when set, else "not pre-start and not
// terminal".
func (s StoryAudit) editable(status string) bool {
	if s.Editable != nil {
		return s.Editable(status)
	}
	st := strings.TrimSpace(status)
	return st != "" && st != "backlog" && st != "ready" && st != "done" && st != "cancelled"
}

// ciPayload is the structured payload of a ci_result row (order:8).
type ciPayload struct {
	Stage  string `json:"stage"`
	Result string `json:"result"`
	Ref    string `json:"ref"`
}

// Audit runs every detector over one story's captured ledger trail and returns
// the findings (empty when the trail is clean). Entries are processed
// oldest-first so "preceding"/"follow-up" checks are order-correct regardless of
// input order.
func Audit(s StoryAudit) []Finding {
	sorted := append([]LedgerEntry(nil), s.Entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.Before(sorted[j].CreatedAt) })

	var out []Finding
	add := func(anomaly, severity, detail string) {
		out = append(out, Finding{StoryID: s.StoryID, Anomaly: anomaly, Severity: severity, Detail: detail})
	}

	auditStatusDrift(s, sorted, add)
	auditShipThenNoGate(s, sorted, add)
	auditSpuriousReject(s, sorted, add)
	auditRejectNoFollowUp(s, sorted, add)
	auditUngatedDeploy(s, sorted, add)
	auditUnengagedWork(s, sorted, add)
	return out
}

// AuditAll runs Audit over many stories and concatenates the findings — the
// project-wide sweep.
func AuditAll(stories []StoryAudit) []Finding {
	var out []Finding
	for _, s := range stories {
		out = append(out, Audit(s)...)
	}
	return out
}

type emit func(anomaly, severity, detail string)

func parseSpine(e LedgerEntry) spinePayload {
	var p spinePayload
	if len(e.Payload) > 0 {
		_ = json.Unmarshal(e.Payload, &p)
	}
	return p
}

// status-drift: documents.status (CurrentStatus) ≠ the latest
// status_transition.to_status. The status must equal the last transition the
// loop recorded; a mismatch means the row and the projection diverged.
func auditStatusDrift(s StoryAudit, sorted []LedgerEntry, add emit) {
	lastTo := ""
	for _, e := range sorted {
		if e.Kind == kindStatusTransition {
			if p := parseSpine(e); strings.TrimSpace(p.ToStatus) != "" {
				lastTo = p.ToStatus
			}
		}
	}
	if lastTo != "" && s.CurrentStatus != "" && lastTo != s.CurrentStatus {
		add(AnomalyStatusDrift, SeverityError,
			fmt.Sprintf("documents.status=%q but the latest status_transition.to_status=%q", s.CurrentStatus, lastTo))
	}
}

// ship-then-no-gate: a status_transition to a ship status (done/deploy) with no
// preceding review_accept for the same from_status — the change shipped without
// a gate's accept.
func auditShipThenNoGate(s StoryAudit, sorted []LedgerEntry, add emit) {
	for _, e := range sorted {
		if e.Kind != kindStatusTransition {
			continue
		}
		p := parseSpine(e)
		if !s.terminal(p.ToStatus) {
			continue
		}
		accepted := false
		for _, a := range sorted {
			if a.Kind != kindReviewAccept {
				continue
			}
			if a.CreatedAt.After(e.CreatedAt) {
				continue
			}
			ap := parseSpine(a)
			if ap.FromStatus == p.FromStatus || (strings.TrimSpace(ap.ToStatus) != "" && ap.ToStatus == p.ToStatus) {
				accepted = true
				break
			}
		}
		if !accepted {
			add(AnomalyShipThenNoGate, SeverityError,
				fmt.Sprintf("status_transition %s→%s with no preceding review_accept", p.FromStatus, p.ToStatus))
		}
	}
}

// spuriousRejectSignals are high-precision substrings that mark a reject as an
// infrastructure failure (a reviewer-key 401 from concurrent runs, sty_98a9bc0a)
// rather than a genuine content rejection. Kept narrow on purpose: broad terms
// like "reviewer key" or "concurrent" match rejects that merely DESCRIBE that
// machinery and produced false positives on the order:9 dogfood — the real
// signal is the auth failure itself (a 401 / unauthorized / an enactment that
// failed to write).
var spuriousRejectSignals = []string{"401", "unauthorized", "unauthorised", "enact-failure", "enactment failed"}

func auditSpuriousReject(s StoryAudit, sorted []LedgerEntry, add emit) {
	for _, e := range sorted {
		if e.Kind != kindReviewReject {
			continue
		}
		body := strings.ToLower(e.Body)
		for _, sig := range spuriousRejectSignals {
			if strings.Contains(body, sig) {
				add(AnomalySpuriousReject, SeverityWarn,
					fmt.Sprintf("review_reject looks like an enact-failure, not a content rejection (matched %q): %s", sig, snippet(e.Body)))
				break
			}
		}
	}
}

// reject-with-no-follow-up: the loop rejected and then went quiet — the latest
// review_reject has no later review_requested/review_accept/status_transition,
// and the story has not reached done. A stalled story.
func auditRejectNoFollowUp(s StoryAudit, sorted []LedgerEntry, add emit) {
	var lastReject time.Time
	for _, e := range sorted {
		if e.Kind == kindReviewReject {
			lastReject = e.CreatedAt
		}
	}
	if lastReject.IsZero() || s.terminal(s.CurrentStatus) {
		return
	}
	for _, e := range sorted {
		switch e.Kind {
		case kindReviewRequested, kindReviewAccept, kindStatusTransition:
			if e.CreatedAt.After(lastReject) {
				return // followed up
			}
		}
	}
	add(AnomalyRejectNoFollowUp, SeverityWarn,
		"a review_reject was not followed by another review_requested and the story is not done — the loop may have stalled")
}

// ungated-deploy: a successful deploy ci_result for a story whose status is not
// done — code shipped whose story is not closed. Newly detectable because
// order:8 lands the CI outcome on the ledger, linked to its story.
func auditUngatedDeploy(s StoryAudit, sorted []LedgerEntry, add emit) {
	for _, e := range sorted {
		if e.Kind != kindCIResult {
			continue
		}
		var c ciPayload
		if len(e.Payload) > 0 {
			_ = json.Unmarshal(e.Payload, &c)
		}
		if strings.TrimSpace(c.Stage) == "deploy" && strings.TrimSpace(c.Result) == "success" && !s.terminal(s.CurrentStatus) {
			add(AnomalyUngatedDeploy, SeverityError,
				fmt.Sprintf("deploy succeeded (%s) but the story status is %q, not terminal", c.Ref, s.CurrentStatus))
		}
	}
}

// processSpineKinds are the rows that mark a story as having actually moved
// through the reviewer loop — the evidence that "work" happened. story_created /
// story_updated are lifecycle metadata, NOT process activity.
var processSpineKinds = map[string]bool{
	kindStatusTransition: true,
	kindStepSummary:      true,
	kindReviewAccept:     true,
	kindReviewReject:     true,
	kindCIResult:         true,
}

// unengaged-work: a story that has started (in_progress) or shipped (done) and
// shows real process activity (a transition/summary/gate/CI row) yet carries no
// review_requested — work advanced without a gate ever being requested. The
// process-activity guard is what keeps this honest: a legacy story whose ledger
// is only story_created/story_updated never entered the loop and is NOT flagged
// (it would otherwise drown the real signal — observed on the order:9 dogfood,
// 59 legacy stories). Best-effort: the spike marked full coverage server-side
// (engagement-store cross-reference); this is the ledger-visible proxy.
func auditUnengagedWork(s StoryAudit, sorted []LedgerEntry, add emit) {
	if !s.editable(s.CurrentStatus) && !s.terminal(s.CurrentStatus) {
		return
	}
	sawProcess := false
	for _, e := range sorted {
		if e.Kind == kindReviewRequested {
			return
		}
		if processSpineKinds[e.Kind] {
			sawProcess = true
		}
	}
	if !sawProcess {
		return // legacy/metadata-only ledger — never entered the loop, not an anomaly
	}
	add(AnomalyUnengagedWork, SeverityWarn,
		fmt.Sprintf("story is %q with process activity but no review_requested in its ledger — work may have advanced unengaged (partial check)", s.CurrentStatus))
}

// snippet trims a body to a short single-line preview for a finding detail.
func snippet(body string) string {
	b := strings.TrimSpace(strings.ReplaceAll(body, "\n", " "))
	if len(b) > 120 {
		return b[:117] + "…"
	}
	return b
}
