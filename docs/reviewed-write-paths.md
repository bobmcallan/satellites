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

## Non-forgeability — decision: accept client-trust (sty_a6d9a0fd)

"An accept happened" is **client-trust**, and the property spans BOTH write channels:

- **Document attestation** — a client that knows the body hash can forge
  `{decision:accept, content_sha256:…}` on a `document_upsert` (`document.go:233`).
- **Status enact via `ledger_append`** — an api-key caller can write a
  `status_transition` (and `review_accept`) row directly, self-completing a story
  or task without a real gate. This is how `tsk_28f62807` was hand-stamped to
  `complete`. The gate's OWN enact uses the same `ledger_append` path under the
  operator's admin auth (`ledger.go:238`), so the two are indistinguishable at the
  verb layer — the same client-trust property.

**Decision: accept client-trust; do NOT add server-side review.** Rationale: the
enact credential is the operator's own admin key — forging an accept requires
already holding it, at which point the holder could equally run the real gate, so
the attestation raises no privilege. True non-forgeability would require the SERVER
to run the reviewer (`claude -p`), which the constitution forbids (mechanism-only
server; gates run client-side on the operator machine where the worktree + `claude`
live). The boundary holds because the executor will not breach it — the
`reviewer-only-model` principle, not a server check. The hand-stamp risk is
mitigated at the point of use by the discoverability fix (sty_4300e117): the tooling
now names the real gate from any state, so an agent no longer needs to route around
a gate it could not find.

## Edge raw-write audit (sty_a6d9a0fd)

| Helper | Reviewed type? | Status |
| --- | --- | --- |
| workspace-objective write (`cmd_workspace.go`) | no — a free-form `type:document`, `scope:workspace` row (no behaviour kind, no `principles:` tag) | **exempt by design** — `reviewRequiredKind`=false; not a gated artifact |
| `setPrincipleAlways` (`cmd_context_curate.go`) | yes — a `principles:`-tagged write | **REGRESSION** — `context curate --drop/--restore` toggles `principles:always` (a TAG/metadata-only patch, body unchanged) but the barrier (`reviewRequiredKind`=true on the `principles:` tag) refuses it without an attestation. Its unit test mocks dispatch, so CI never caught it. Fix tracked separately: exempt a body-unchanged metadata patch from the CONTENT barrier. |

## See also

- `sty_dc44e359` — the `context curate` barrier-regression fix.
- `sty_4300e117` — discoverable gates (the at-use mitigation for the status-enact channel).
