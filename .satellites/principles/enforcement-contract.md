---
name: enforcement-contract
tags: [principles:global, process, enforcement]
---
# Enforcement contract: satellites enforces, the user defines

`done` is reachable ONLY by passing every gate the user's workflow defines, in
order. The agent cannot select its own subset of the path.

- **No default workflow.** satellites ships the enforcement engine, never a
  built-in workflow the agent can satisfy minimally or fall back to. With no
  compliant definition loaded, the agent fails closed and cannot proceed.
- **No un-gated required steps.** Anything the user requires — review gates, a
  commit-push / ship checkpoint, a techdebt / integration gate — must be a gated
  transition in the definition, not prose or a "trusted capability". An un-gated
  step is a skippable step.
- **Config over code.** The required path is expanded by the user's definition,
  never by hard-coding a richer default into the product.
