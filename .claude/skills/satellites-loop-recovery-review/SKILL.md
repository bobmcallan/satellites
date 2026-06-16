---
name: satellites-loop-recovery-review
type: skill
kind: gate
when: recovery-requested
tags: [kind:gate]
description: Gate skill for recovering a fail-loop-exhausted story — accepts a move blocked → in_progress only when the story is blocked AND its body carries a concrete recovery rationale showing the block was a recoverable FLAKE (named, and fixed), not a genuine ×3 product failure. Emits {decision, notes} JSON.
---
<!-- satellites-sync:begin {"document_id":"doc_fff68d21","version":1,"hash":"b096b1679c2792ab6c2678950804411745364b36df7593f114e1758dc76a1513","publisher":"proj_682cfeed"} satellites-sync:end -->
<!-- satellites-library:begin {"publisher":"proj_682cfeed","repo":"git@github.com:bobmcallan/satellites-skills.git","commit":"45628d3a97a4328fdd77aa83cb11ec77ee432dd0"} satellites-library:end -->

Decide whether a `blocked` story should be returned to `in_progress` to fix and re-run. A story reaches `blocked` only by exhausting a review fail loop (×N rejects). That escalation is the operator's to lift — this gate is the sanctioned, reviewer-judged path back, so a flake-blocked story is not a dead end. Recovery is orthogonal to the forward workflow — the target is fixed: `from_status` is `blocked`, `to_status` is `in_progress`. The reject count auto-resets (the `exhausted` marker already zeroes it), so the recovered story re-enters its review loop with a fresh quota.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`, `story_status` (current state), and `story_body` (the full story markdown).

The gate's `.satellites/satellites exec` calls authenticate as the operator's admin user, authorized to write status_transition / review_* rows.

## What to check

You assess the recovery rationale — you do not run the build or tests.

- **Reject** when `story_status` is not `blocked` — only a fail-loop-exhausted block is recoverable here. Say so in the notes.
- **Reject** when the body carries no concrete recovery rationale. The requester must state one in a `## Recovery` section that establishes ALL of:
  - **the cause was a FLAKE, not the story's own defect** — name the specific test(s)/check(s) that failed the loop, and the evidence it was flaky or unrelated (e.g. it passes deterministically in isolation; different tests failed across the iterations; the failure was environmental/timing, not the story's change), AND
  - **the flake is fixed** — name the concrete fix applied (a deterministic assertion, a registered quarantine, a tightened timing), not "re-ran and it passed", AND
  - **the story's own work is sound** — its acceptance criteria are met / its own tests pass.

  A vague rationale ("it was flaky", "just retry", no section, or an admission that the ×3 was a real product failure) is a reject — name the gap. A genuine ×3 product failure is NOT recoverable here; it stays blocked for the operator.
- **Accept** when `story_status` is `blocked` AND the `## Recovery` section concretely establishes a named, fixed flake (not a real failure) and sound underlying work.

## Enact

You enact your decision, you do not just report it. Your target is fixed: `to_status = "in_progress"`, `from_status = "blocked"`. Run these with Bash before printing your decision.

**On accept** — two `ledger_append` calls:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"blocked","to_status":"in_progress","gate":"satellites-loop-recovery-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"blocked → in_progress","payload":{"from_status":"blocked","to_status":"in_progress"}}'
```

The `status_transition` row IS the status change.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-loop-recovery-review"}}'
```

If the status_transition `ledger_append` fails, the recovery did not land — print `reject` with the failure as the reason.

## Environment

Runs as a `recovery-requested` gate over one blocked story's JSON on stdin, writing to the ledger via `.satellites/satellites exec` under the operator's admin auth. It mutates external state (review rows and one status transition), so its tool use is bounded by the guardrails below.

```yaml
guardrails:
  always:
    - Target is fixed — to_status=in_progress, from_status=blocked; never resolve a target from `## Workflow`.
    - On accept, write both ledger_append rows (review_accept then status_transition); the status_transition row is what enacts the change.
    - Verify the status_transition ledger_append succeeded before printing accept; on its failure print reject with the failure as the reason.
    - Emit exactly one {decision, notes} JSON object as the only output.
  ask_first: []
  never:
    - Never call document_upsert — this gate does not edit the story body.
    - Never write a status_transition row on reject.
    - Never accept a story whose story_status is not `blocked`.
    - Never accept on a vague rationale, or when the ×3 failure was a genuine product defect rather than a named, fixed flake.
```

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific gap — not blocked, or a missing/vague recovery rationale, or a genuine (non-flake) failure.
