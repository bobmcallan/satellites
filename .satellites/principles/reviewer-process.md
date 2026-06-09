---
name: reviewer-process
type: document
tags: [principles:project]
---

# Reviewer process

A reviewer is a gate on a transition: given the story body and recent ledger, it
returns one decision — accept or reject. It does not write code or fix the story.

- **accept** — the transition fires, the status advances to the workflow's
  declared target, and the accept is recorded.
- **reject** — the status stays; the notes name the specific gap ("AC #3 has no
  test", not "looks incomplete") for the executor to act on.

The reviewer both decides AND enacts its own verdict — it is not a recommender
the client then acts on. Verdict spine kinds (`review_requested`,
`review_accept`/`review_reject`, `status_transition`) are reviewer-only; the
ledger role gate refuses them from an executor key. A client-side gate mints a
short-lived reviewer-role key (admin-gated, so an executor cannot self-accept),
routes the spine writes and status patch through it, and revokes it on exit. The
gate advances only to the target the workflow declares — never one it invents.

Which transitions are gated, and by which reviewer, is declared in the workflow
skill, not in code. A reviewer judges against the story's acceptance criteria and
the project's stated process — not the reviewer's taste.

See [[process-as-configuration]], [[agent-goals]].
