---
name: reviewer-quarantine
type: document
tags: [area:enforcement-surface, area:reviewer, kind:reference, content-review:allow-refs]
---

# Reviewer quarantine — owned-failure reconciliation

The shared rule a reviewer gate applies when it runs a functional `check:` and
some checks fail: ship only when the tree is **clean OR every failing check is
owned debt named in a quarantine register**. This is a GENERIC reviewer-gate
capability, not a property of any one gate. `technical-debt-review` is its
current (only) consumer; a future reviewer gate that runs a functional check
reconciles the same way against the same register.

The reconciliation is **substrate, not binary**: the harness runs the gate's
`check:` and injects the result; the reviewer (the gate skill) reads the register
document and applies the rule below. The binary holds only the run mechanism — no
reconciliation branch lives in Go ([[satellites-constitution]]).

## The register

A quarantine register is a document whose rows are `| check_id | story_id |
reason |`. Each row is a **known, owned** failing check. The
[[technical-debt-register]] is the register `technical-debt-review` reads.

- The register **only shrinks**: when a quarantined check goes green, its row is
  removed (the owning story closes the window). It grows only as a deliberate,
  story-backed capture of a failure that cannot be fixed in the moment.
- A row with **no `story_id` is unowned** — it may not pad the register to dodge
  the gate.

## The reconciliation rule

From the gate's injected functional-check output, judged against the register:

- **A failing check** → reject UNLESS its `check_id` is a register row with a
  non-empty `story_id` (registered + owned → tolerated).
- **A build/compile failure** → reject. A broken build is never registerable.
- **An unowned register row** (no `story_id`) → reject.
- **A registered check that PASSED this run** → it is stale; accept, and name it
  so the owner removes the row (the register only shrinks).
- **Fail closed**: if the injected check result or the register cannot be read,
  reject with the reason named.

A reviewer gate that adopts this capability names its own register and runs its
own `check:`; the rule above is identical. See [[broken-windows]],
[[satellites-constitution]].
