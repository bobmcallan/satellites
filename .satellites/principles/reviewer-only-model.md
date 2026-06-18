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

## Authority is not yours to take

The structural gate is the floor, not the whole rule. The executor's job is to
drive a story to `done` **through the gate**: run the project's review at each
transition (`satellites story review <id>`) and iterate on rejection. Requesting
review is not optional, and it is not the operator's job to initiate — it is how
an executor finishes work. **Running the gate is not taking authority.** The gate
spins up a fresh-context reviewer that decides independently and enacts the status
under its own reviewer key; you are asking for judgement, not rendering it. The
ephemeral reviewer key the gate mints to do that is part of the sanctioned
mechanism, not a credential you are wielding.

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
