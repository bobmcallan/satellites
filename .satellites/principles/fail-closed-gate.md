---
name: fail-closed-gate
type: document
tags: [principles:project]
---

# A gate fails closed

A reviewer gate decides one thing: may this story leave its current state? It
answers by *verifying*, never by trusting the story's claims.

A workflow may declare a transition ungated — a deliberate fast path where the
gate passes through with no review. This principle does not require every
transition to be gated; it binds any gate that *does* review. Once a gate
verifies, it fails closed.

## Verify, don't trust

A criterion that says "a test exists" is met only when that test runs and passes
under the reviewer's own hand. Read the diff, run the build, run the tests, read
the rows the criteria reference. An assertion in the body is evidence of intent,
not of completion.

## Inability to verify is a reject

If the gate cannot run the check it needs — the build won't run, a tool is
missing, a row can't be read — that is a **reject** with the reason named, not a
soft pass. "Could not execute" never rounds up to accept. When in doubt, reject:
a false accept ships half-done work; a false reject costs one more iteration the
executor can act on.

## Name the gap

A rejection states the specific unmet thing — "AC #3 has no test", not "looks
incomplete". The executor reads the notes verbatim and iterates.

## An accept that didn't land is a reject

The gate enacts its own verdict (see [[reviewer-process]]). If the write that
records the transition fails, the move did not happen — print reject with the
failure as the reason rather than claiming an accept that never took.

See [[reviewer-process]], [[agent-goals]].
