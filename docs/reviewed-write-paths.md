# Reviewed-artifact write paths — the single governed path

Every write of a **behaviour-kind** artifact (skill, workflow-as-skill, principle)
funnels through ONE choke-point that refuses an un-reviewed write. This document
enumerates the paths, marks gated vs. raw, and records the residual gaps. It is the
map sty_4c0cf727 (epic `reviewed-write-integrity`) was raised to produce.

## The choke-point

All document writes go through the single verb `invokeDocumentUpsert`
(`internal/verb/document.go:950`). Its branches:

| Mode | Trigger | Writer |
| --- | --- | --- |
| 1 — patch by id | `req.ID != ""` | `upsertByID` → `UpdateStory` / `upsertTaskByID` (story/task ONLY; any other type is **refused**, `document.go:1256`) |
| 2 — create story | `type=story` | `createStory` → `store.CreateStory` |
| 2b — create task | `type=task` | `createTask` → `store.CreateTask` |
| 3 — key-addressed | `(scope,name)` | `store.Upsert` |

The behaviour-kind review barrier sits in front of all of them
(`document.go:988`):

```go
if req.ID == "" && reviewRequiredKind(req) {       // skill / workflow / principle
    if err := verifyReviewAttestation(req); err != nil { return nil, err }
}
```

`verifyReviewAttestation` requires a `review` block with `decision==accept` and a
`content_sha256` bound to the body (rejecting stale/replayed accepts). The CLI
`upload` commands mint that attestation **after** running the per-type reviewer
client-side (`claude -p`). The raw verb does not review, so an un-attested
behaviour write is refused with a pointer to the `upload` command (sty_e6226180).
`scope:system` is independently rejected at the store boundary
(`store.Upsert`, `ErrScopeReadonly`); the only sanctioned system writer is the
changelog lane.

## Path status

| Path | Reviewed type | Gated? |
| --- | --- | --- |
| `satellites skill upload` / `publish` | skill, workflow | ✅ reviewer → attestation → upsert |
| `satellites workflow upsert` | workflow | ✅ (shared `uploadKind`) |
| `satellites principle upload` | principle | ✅ (shared `uploadKind`) |
| MCP / `exec document_upsert` (id-less, behaviour kind) | skill/workflow/principle | ✅ refused without attestation |
| MCP / `exec document_upsert` id-patch | non-story/task | ✅ refused (`upsertByID` type guard) |
| `document_upsert` `{status}` by api-key | story | ✅ status field dropped; status moves only via the gate's `status_transition` ledger row (sty_42d13ae4) |
| `document_upsert` create story/task | story, task | ⚪ ungated AT CREATE **by design** — gated at the lifecycle entry transition (`satellites-intent-plan-review` / `satellites-task-upsert-review`) |
| `document upload` (free-form, untagged) | document | ⚪ no per-type reviewer (not a behaviour kind) |

⚪ = intentionally not review-gated at write (lifecycle- or not-a-behaviour-kind).

## No duplicate writers

There is one verb (`invokeDocumentUpsert`) and one store write per mode
(`Upsert` / `CreateStory` / `CreateTask` / `UpdateStory`). No second code path
writes a documents row, so the barrier cannot be sidestepped by a parallel writer.

## Residual (tracked as a follow-up)

Two items remain and are **not** a single bounded change — they carry design
decisions, so they are split out rather than forced here:

1. **Non-forgeability.** The attestation is client-trust: a client that knows the
   body hash can forge `{decision:accept, content_sha256:…}`. True
   non-forgeability needs SERVER-SIDE review (the server running the reviewer),
   which has constitution implications (the server holds mechanism; gates run
   client-side). Noted at `document.go:233`.
2. **Edge raw-write paths to verify.** A few CLI helpers issue a key-addressed
   `document_upsert` directly (e.g. `setPrincipleAlways` in
   `cmd_context_curate.go`, the workspace-objective write in `cmd_workspace.go`).
   A `principles:`-tagged write hits the barrier and is refused without an
   attestation, so these need case-by-case verification — confirm each either
   carries an attestation, is exempt by design, or must be routed.
