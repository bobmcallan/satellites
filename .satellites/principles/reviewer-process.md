---
name: reviewer-process
type: document
tags: [principles:project]
---

# Reviewer process

A reviewer is a gate on a transition, nothing more. It answers one
question: may this story move to the next state? It does not write the
code or fix the story — it decides: accept or reject. On accept the
story advances to the next state and the verdict is recorded; on reject
the status stays and the notes tell the executor what to fix.

## The contract

The substrate runs the gate skill named on a transition and hands it
the story body and recent ledger. The gate returns one decision:

- **accept** — the transition fires, the status advances, and the
  accept is recorded in the ledger.
- **reject** — the status stays; the rejection notes are recorded for
  the executor to read and act on.

A gate names the specific gap on reject. "Looks incomplete" is not a
review; "AC #3 has no test" is.

## Reviewer authority is a key role

The spine kinds a verdict produces — `review_requested`,
`review_accept` / `review_reject`, and `status_transition` — are
reviewer-only. The ledger role gate refuses them from an executor- or
runner-role key; an executor can write `log:` rows and nothing more. So
the authority to record a verdict and advance a story is a property of
the api-key's role, not of who is running.

A gate that runs **client-side** (on the operator machine, where the
worktree and `claude` live) therefore cannot use the operator's
executor key to record its decision. For the duration of the run it
mints a short-lived **reviewer-role** key, routes the spine writes and
the status patch through that key, and revokes it when the run ends.
Minting a reviewer key is admin-gated: an autonomous executor cannot
mint one and self-accept. The decision is made where the code is; the
authority to enact it stays real.

See [[ledger-spine]].

## The reviewer decides AND enacts

A reviewer is not a recommender the client then acts on — it both
decides and **enacts** its own verdict. The minted reviewer key is set
in the gate subprocess's environment (`SATELLITES_REVIEWER_API_KEY`),
so the gate skill itself calls `satellites exec` to patch the status
(on accept) and append the `review_accept` / `review_reject` and
`status_transition` spine rows. The client orchestrates — it resolves
the story, picks the gated transition for context, mints and revokes
the reviewer key, and records that the gate was *requested* — but it
does not patch the status or write the verdict. Enactment is
configuration in the skill, not a branch in the client.

This keeps every gated transition on one template: a reviewer is a
skill that, given a story and its workflow target, decides and then
makes the decision real. Adding a gate means authoring a skill that
follows the template; it never means adding enactment code to the
client. The guardrail is the workflow: the skill advances only to the
state the workflow declares as the transition's target — it is the
*enactor* of the next state, never its inventor.

## Reviews are configuration

Which transitions are gated, and by which reviewer, is declared in the
workflow skill — not in code. A gate is itself a skill the admin
authors. Add a gate by naming it on a transition; change a review by
editing its skill. Same mechanism as the process it guards.

## What a reviewer judges against

The story's acceptance criteria and the project's stated process — not
the reviewer's taste. A story that meets its criteria passes, even if
the reviewer would have done it differently.

See [[process-as-configuration]], [[agent-goals]].
