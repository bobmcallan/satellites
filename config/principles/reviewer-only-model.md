---
name: reviewer-only-model
tags: [principles:global]
---
# The reviewer-only execution model

satellites runs one model, and every client obeys it. Roles are fixed; the
process is configuration; story status decides what is valid now.

## Roles

- **The agent is the executor.** The local agent does the work. Its one goal
  is to drive a story to `done` — read the story, do what the current state
  calls for, request review at each gated transition, iterate on rejection.
- **satellites is the reviewer, only.** A reviewer is a fresh-context gate on
  a transition: it judges whether the story may advance and **enacts** the
  status change under a reviewer-role key. It does not write the code, fix the
  story, or execute work.

The split is structural, not etiquette: the api-key role gate refuses a status
transition from an executor key. The agent cannot self-advance; the reviewer
does not do the work. Neither role can take the other's move.

## One enforcement primitive — the reviewer

satellites enforces exactly ONE thing: a reviewer's accept on a transition.
There is no second enforcement concept. A **reviewer** is the only kind that
gates a transition; the former `gate` and `capability` kinds are retired — a
"gate" was a reviewer by another name, and a "capability" was advisory guidance
the agent was never bound to. Everything that is not a reviewer is a non-binding
**guide**.

- **WHAT, not HOW.** A reviewer judges the OUTCOME the story claims; it never
  prescribes the procedure. The agent reaches `done` any way it can — the only
  move it cannot make is to advance status without the reviewer's accept.
  satellites does not script the agent's steps, inject deterministic actions, or
  assume a fixed method. It judges the result, and the goal is simply: the story
  is `done`.
- **Guides bind nothing.** Documents and principles (this one included) inform
  the agent; they do not gate. Guidance not enforced by a reviewer is advice —
  the agent may ignore it and still reach `done`, and a reviewer is the only
  thing that can hold back a result that fails the bar.
- **Pre-reading is the harness's concern.** Whether the agent studies the right
  context before acting is the client/harness's job — and it improves with the
  model — not satellites'. satellites does not try to guarantee the agent read a
  skill first; it guarantees a reviewer judges what the agent produced. If the
  agent did not meet the reviewer's measure, it fails — that is the whole
  contract.
- **One reviewer mechanism.** A reviewer is one configurable `claude -p`
  invocation; there is a single such invocation in the binary, reused for every
  review — never a second, divergent copy.

*Why:* an advisory layer the agent can bypass is not enforcement, and a second
enforcement concept is a second thing to drift. Collapsing to "the reviewer is
the only gate; everything else is a guide" makes the boundary absolute and the
substrate honest — the one thing that blocks is the one thing that is reviewed.

## Authority is not yours to take

The structural gate is the floor, not the whole rule. The executor's job is to
drive a story to `done` **through the gate**: run the project's review at each
transition (`satellites story status_transition`) and iterate on rejection.
Requesting review is not optional, and it is not the operator's job to initiate —
it is how an executor finishes work. **Running the gate is not taking authority.**
The gate spins up a fresh-context reviewer that decides independently and enacts
the status under its own reviewer key; you are asking for judgement, not rendering
it. The ephemeral reviewer key the gate mints to do that is part of the
sanctioned mechanism, not a credential you are wielding.

What you must never do is wield reviewer authority **yourself** to move a story
past a gate: do not hand-mint or reuse a reviewer credential to patch a status,
do not relabel a story to dodge a gate, do not write a verdict or set a status by
any path the role gate left open. The forbidden move is *routing around* the gate
— never *running* it. If the gate will not pass, the work is not done: fix what
it named, or surface the gap.

A control you *could* slip is still a line you do not cross — the reviewer
boundary holds because the executor will not breach it, not only because a check
blocks it. But the opposite failure is just as real: an agent that ships its work
and then **declines to run the gate** — leaving the story ungated while the code
is live — has abandoned the job, not protected the boundary. Two failures, one
rule: advance your story *only* through the gate's accept, and *always* through
it. Drive to done; let the reviewer decide.

## Process is skills

The states a story moves through, their order, and which transitions a reviewer
gates are **skills**, configured per project — not branches in the binary.
Change the skill, change the process; no release.

## Status gates what is valid

A story's **status** decides which skill and which transition apply. The agent
does not choose freely.

To work a story: list the substrate skills (system + workspace + project),
match the story's `type` and `status` against each skill's `applies_to` /
`when`, load **only** that matching skill's body, and follow it. The
status-gated match is authoritative for which transition is valid — do not
invoke a skill by its description alone and skip the gate. The skill list is
the index; read it fresh, do not cache.

A transition fires only when the reviewer named on it accepts; `done` is the
terminal status reached with every gate on the path accepted — nothing else is
done.

## The story is the contract

At planning, instantiate the matched workflow and the plan **into the story
body**, so the reviewer reads one self-describing artifact. The requirements an
executor must satisfy and the criteria a reviewer checks are the same thing,
recorded once — in the story. Plan-review validates the embedded workflow
against its source skill; later gates follow the workflow the story records.
