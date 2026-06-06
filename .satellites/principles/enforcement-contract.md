---
tags: [principles:project, process, enforcement]
---
# Enforcement contract: satellites enforces, the user defines

The product's value IS enforcement of the user's process. If an agent can
traverse a **subset** of the defined path and still reach `done`, satellites has
failed — the process is then advisory, not enforced. A user who defines 15 gates
and gets 6 has no product.

## The contract
1. **No default workflow.** satellites ships the enforcement ENGINE (gate runner,
   START door, audit) — never a built-in workflow the agent can satisfy minimally
   or fall back to. The workflow (every state, gate, required step) is the user's
   config. No compliant definition loaded ⇒ the agent **fails closed** and cannot
   proceed.
2. **No un-gated required steps.** Anything the user requires — review gates, a
   commit-push / ship checkpoint, a techdebt / integration gate — must be a
   **gated transition** in the definition, not prose or a "trusted capability" the
   agent is merely expected to run. An un-gated step is a skippable step (proven
   live: an agent shipped nine stories without ever running `/commit-push` or the
   integration tier — both prose-only).
3. **Total traversal.** `done` is reachable ONLY by passing every gate the user
   defined, in order. The agent cannot select its own subset.
4. **Config over code.** The required path is expanded by the user's definition,
   never by hard-coding a richer default into the product.

## This contract will be wrong first — observability is how it becomes right
Which gates are required, where a non-status-change checkpoint (e.g. ship) sits,
and what each gate verifies are unknowns. We do NOT get this right by thinking
harder up front; we get it right by **observing** where the agent diverges and
adjusting. The contract is NOT frozen — it is iterated against
`epic:qa-observability`: make the complete delivered context visible + reviewable,
watch divergence, adjust. **Enforcement without observability is blind;
observability is how the contract becomes correct.**

> Scoped `principles:project`, deliberately NOT `principles:always` — this governs
> how the *product* enforces; it is not agent-turn ride-along. Growing the
> executor's in-context rulebook is the failure mode `qa-observability` exists to
> reverse.
