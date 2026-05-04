// Package service is the standalone reviewer worker that consumes
// kind:review tasks from the task queue, runs the configured reviewer
// against the rubric + evidence, and writes the verdict directly:
// a kind:verdict ledger row tagged to the review task, a task close
// with success/failure outcome, and on rejection a successor work +
// paired planned-review task pair (sty_c6d76a5b slice A).
//
// Today the worker runs as an in-process goroutine wired by
// cmd/satellites/main.go. The mode is sourced from the system-tier
// KV row `reviewer.service.mode` (default "embedded") — application
// behaviour belongs in the substrate, not in process env or
// infrastructure secrets. The shape is forward-compatible with a
// separate-process worker (ModeExternal) — the queue + role-grant
// primitives both work equally well across processes.
//
// epic:v4-lifecycle-refactor sty_6077711d / sty_62d4b438.
package service

// Mode enum values for the system-tier KV row `reviewer.service.mode`.
//
//   - ModeEmbedded runs the reviewer as an in-process goroutine.
//   - ModeExternal is the placeholder for a separate-process worker
//     (deferred per sty_6077711d AC; embedded is enough for slice
//     closure).
//   - ModeDisabled skips wiring entirely; the close path stays on the
//     legacy inline reviewer.
const (
	ModeEmbedded = "embedded"
	ModeExternal = "external"
	ModeDisabled = "disabled"
)

// ServiceSessionID is the stable session-registry id used at boot for
// the embedded reviewer service. The service's task_claim and ledger
// writes run under this session id.
const ServiceSessionID = "session_reviewer_embedded"

// ServiceUserID is the system identity that owns the reviewer
// service session. Mirrors project.DefaultOwnerUserID — system-issued
// sessions are not tied to a real user.
const ServiceUserID = "system"

// WorkerID is the value the reviewer service stamps as ClaimedBy on
// every task it picks up. Distinct from ServiceSessionID so log
// queries can distinguish "session that holds the grant" from
// "worker that owns the task".
const WorkerID = "reviewer_service_embedded"
