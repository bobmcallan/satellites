// Package audit reconciles a story's ledger stream against the
// process-adherence model (sty_6e271170, epic:process-adherence) and flags the
// anomalies the SOFT advisory story-access trigger deliberately lets through.
// It is the detection half of the "narrow at the trigger + detect the residual"
// posture: prevention is best-effort and fast; this catches what slips. Pure and
// data-driven so it can run server-side over synced engagement events plus the
// existing ledger rows — the broader surfacing lives under qa-observability.
package audit

import "sort"

// Entry is the minimal ledger/engagement row the reconciler inspects.
type Entry struct {
	Story   string
	Kind    string
	Session string
}

// Finding is one flagged process-adherence anomaly.
type Finding struct {
	Story   string
	Code    string
	Message string
}

// EngageKind is the ledger kind a `work init` flush writes (engagement events
// ride ledger_append as engagement:<kind>).
const EngageKind = "engagement:engage"

// CodeUnengagedWork marks a story with work activity but no engagement.
const CodeUnengagedWork = "unengaged-work"

// isWorkKind reports whether a ledger kind represents actual work/progress on a
// story (as opposed to engagement bookkeeping or reads). status_transition and
// review_accept are the gated-progress rows; commit/evidence are work artifacts.
func isWorkKind(kind string) bool {
	switch kind {
	case "status_transition", "review_accept", "commit":
		return true
	}
	// evidence rows (evidence, evidence:test, …) are work artifacts.
	return len(kind) >= 8 && kind[:8] == "evidence"
}

// FindUnengaged flags every story that shows work activity (a status_transition,
// review_accept, commit, or evidence row) with NO engagement:engage event in the
// stream — i.e. work that bypassed the engage step the advisory trigger asked
// for. Deterministic: findings are sorted by story id.
func FindUnengaged(entries []Entry) []Finding {
	engaged := map[string]bool{}
	worked := map[string]bool{}
	for _, e := range entries {
		if e.Kind == EngageKind {
			engaged[e.Story] = true
		}
		if isWorkKind(e.Kind) {
			worked[e.Story] = true
		}
	}
	var out []Finding
	for story := range worked {
		if !engaged[story] {
			out = append(out, Finding{
				Story:   story,
				Code:    CodeUnengagedWork,
				Message: "story shows work activity (status_transition/commit/evidence) but was never engaged (no engagement:engage)",
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Story < out[j].Story })
	return out
}
