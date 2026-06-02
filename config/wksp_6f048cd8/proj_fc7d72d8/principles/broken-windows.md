---
name: broken-windows
type: document
tags: [principles:project]
---

# Broken windows

A failure you encounter is yours to act on. You do not step past it.

## The rule

- **Fix it** when the failure is cheap or sits in the path of your change.
- **Otherwise file it** — a tracked story that names the failure — and surface
  it to the operator. Never silently pass it by.
- **Never add a new red.** A change that introduces a failing test, a skipped
  check, or undocumented debt is not done.
- **Known debt only shrinks.** A quarantined failure is one named in the
  technical-debt register, each entry owned by a story; the register goes down,
  never up.

## Why

A window left broken signals that breakage is tolerated, and the next one is
free to add. Existing norms ([[agent-goals]], [[story-execution-process]])
govern only the story in hand; neither makes a failure you merely walk past
partly yours. This one does — every failure is closed or captured, so the count
stays visible and falling.

## At commit

The commit checkpoint enforces this: the tree is clean, or its debt is a story
the agent created. The `technical-debt-review` pre-commit gate makes the norm
non-optional — it asks for clean-or-a-story, and "it was already broken" is not
a pass. Name it, file it, or fix it.

See [[agent-goals]], [[story-execution-process]].
