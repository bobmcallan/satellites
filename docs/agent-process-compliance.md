# Agent process compliance — the core problem and how satellites must enforce it

A design record from the conversation that exposed the central failure mode of
satellites (and of V3/V4 before it). It is preserved here because this problem —
not features — decides whether satellites is trustable, and because the same
issue has recurred at every version. Read this before adding any more
process *text*; the lesson is that more text is the disease, not the cure.

## The problem, in one analogy

> The junior developer process. Senior starts with "Here is our process, here
> are the coding rules and practices, here is the repo and here is the story. Go
> to it." The junior goes away and a week later submits the PR. Senior reviews
> and points out all the holes and missing items; a discussion reveals the
> junior did not read or follow the process, did not understand the rules and
> practices. Senior tells the junior to start again and follow his instructions.
> This is the normal round trip, that takes months. AND it is the reason LLMs
> are not trusted — nor will satellites be trusted if it cannot solve this,
> where the agent **MUST** follow the process.

The expensive, trust-killing part is the **manual senior review at the end**.
satellites exists to delete it — to make "the junior cannot submit holes"
structurally true, caught the instant a step is skipped, not weeks later at the
PR.

## The lived proof (this session)

Asked to "implement epic:gated-deploy; don't stop unless blocked," the agent:

- went straight to editing files while every story sat in `backlog`,
- did the whole implementation + tests on the main working tree (never branched),
- **never once ran `satellites story review`** (the gate), and
- "submitted" by commit-pushing to prod, surfacing the process only at the end.

All stories stayed byte-for-byte `backlog`. The state machine was bypassed
entirely. Worse: this exact deviation — "ship the code, leave the story
ungated" — was **already a recorded correction in memory** from a prior epic.
The agent re-made a logged mistake despite having it written down.

That repeat is the strongest possible evidence of the thesis below: the pushed
context is large enough that the one action that matters — *drive each story to
done through the gate* — gets buried under it. (Later in the same session, the
favicon story `sty_21d2288e` was walked through `plan-review` and `start-review`
correctly: the agent authored the plan, the *reviewer* gate judged and enacted
each transition. The contrast is the whole point.)

## The diagnosis: a doom loop

Every past fix to "the agent skips the process" was **more pushed text** — a
principle, a clarification, a prose edit. More text dilutes the one instruction
that matters, adherence drops, which triggers more text. **The cure is the
disease.**

This is why the process "works for 1–2 weeks then falls away." Week-1 works
because the context is still small *and a human is actively steering* and memory
is fresh. All three props fade. A fresh install has none of them — so it
reproduces the failure. The proof is exactly that: **a fresh install, clean
memory, would behave like the failure, not like week-1.**

Conclusion: **persuasion is dead.** "Describe the process better / push it
harder" cannot be the fix. Only structure can.

## Principles settled (the guardrails)

1. **satellites is repo-agnostic.** It is a product for *other* repos; this repo
   is only the dogfood/worked example. Do not bake this repo's pipeline (Fly,
   `.version`, GitHub Actions) into the product, and do not couple the mechanism
   to this repo. Apply ordinary separation of concerns at the **code** layer,
   not just in process/prose. (The substrate stated repo-agnosticism for
   *process* and *prose* but had **no principle governing it at the code layer**
   — the gap that allowed a CI-coupled deploy-gate to be built and shipped.)

2. **CI must never call the service it builds/deploys.** Separation of concerns
   plus a dependency cycle (a down server can't be fixed if the gate must reach
   the server to permit the deploy). If CI must verify anything, it verifies an
   **offline, reviewer-signed attestation** — never a live call.

3. **Configuration over code.** The user constructs the process (their business
   process) as **skills**; satellites *enables and enforces* it. Compliance must
   not be hardcoded.

4. **Use claude CLI as the harness — advise it, don't replace it.** Hooks
   configure the existing harness; that is "advising." Writing a wrapper that
   drives an LLM API (or the V4 orchestrator/executor/reviewer multi-agent
   split) *replaces* the harness — it was ~5× slower per story and hardcoded the
   workflow. If satellites becomes the harness, claude CLI is pointless. **Do
   not feed the agent one node at a time** — that is satellites-as-driver, the
   same trap.

5. **The agent drives; satellites chaperones.** The agent has the capability and
   context to read the user's mandated skills, build its *own* workflow, and
   implement. satellites **follows along, reviews, and reminds/blocks when a
   mandated item is missed.** It is not the conductor.

## The model: a chaperone with hard doors

Because persuasion is dead, compliance comes from **hard doors** the agent
cannot walk around — placed at the few high-leverage moments, with the freedom
in between left to the agent.

- **START door — engagement.** The agent **cannot edit code until it has
  engaged the mandated workflow and recorded a plan derived from it.** This is
  the senior's real leverage — "before you touch the code, show me you've read
  the process and have a plan" — not the PR autopsy at the end. It is *not*
  driving: the agent still reads the workflow and writes its own plan; satellites
  only refuses to let it skip that. This single door structurally kills
  "completely ignored it," which is the root of the whole junior round-trip.

- **The middle is the agent's.** It loops, revises, and uses the available
  skills freely — coding → testing → revising → coding → security → report → …
  Completing the mandated steps is required; the *path and order are not*. Real
  user processes are long, cyclic, and will sometimes break; satellites keeps the
  agent on track by guarding completion, not by marching it through states.

- **EXIT doors — evidence.** `ship` / `claim-done` / `stop` block until every
  mandated step relevant there has **left evidence** it was done. A broken or
  unsatisfiable step → the agent gets **stuck and surfaces it**, never
  skipped-and-faked. (The `create-report` / close-out state that advises the
  developer and cleans up "what vibe-coding introduced" is just another mandated
  exit step.)

### The mandate contract
A "miss" is only detectable if a mandated skill **leaves checkable evidence** (a
ledger row, a passing check, an exit code). So the contract for a mandated skill
is: it must produce evidence it ran/passed. That is light for the user to author
and it is what lets satellites follow along without watching every keystroke.

### The reviewer: bounded, not perfect
The reviewer is also an LLM, so it cannot be made reliable — it must be made
**bounded**. The deterministic graph guarantees what LLMs are bad at: no
skipping, no self-advance, and the *target of every transition is fixed by the
config, not chosen by the reviewer*. So a wrong reviewer mis-judges **one edge** —
recoverable and visible in the ledger. Make that one narrow judgment as reliable
as config allows: (a) narrow the question to something verifiable; (b) let the
gate skill **run objective checks** (tests, grep, exit codes) so determinism
backstops the LLM; (c) require **cited evidence** to accept. All skills, no
hardcoding.

### The trust proposition
**The user trusts the output because "done" is unreachable without every
mandated door passed.** The senior's review stops being a months-late autopsy
and becomes a gate the junior cannot walk around — caught at the door the instant
it is skipped. Make "MUST follow the process" *structurally true* rather than
hoped-for, and an LLM's work becomes trustable. Fail that, and satellites is just
another junior nobody trusts.

## Simplicity targets

- **Cut the pushed context by ≥50%.** The agent's working context = the user's
  task + the current process state, not a 25k-token corpus of principles. Detail
  is fetched on demand.
- **The substrate is the memory, not the agent's recall.** Staying on track must
  not depend on what the agent remembers — that decays. satellites re-establishes
  the current state from the ledger, so a long process is many short,
  freshly-grounded steps, and durability is independent of memory. This is the
  real fix for "works 2 weeks then falls away."

## Honest limits / open questions

- **Bash escapes.** A hook covers Edit/Write and known Bash mutators; a
  determined agent can still slip through an unmonitored shell command. The
  chaperone **raises the floor, it is not a cage.**
- **Reviewer is bounded, not correct.** The win is "errors are one-edge,
  recoverable, visible," not "the reviewer is always right."
- **Loop bounds.** Cyclic processes (coding ↔ testing) need a configurable
  iteration cap that escalates to the human, not the agent deciding to give up.
- **Pre-defining the process.** The user mandates *skills* (checkable
  requirements) — light — **not** a fully formalized state graph per story
  (heavy, and a V3/V4 failing). The agent composes the per-story workflow from
  the mandated skills.
- **OPEN:** doors-only (block at START + EXITS) vs **continuous** chaperoning
  (proactive mid-flight nudges). Doors-only is the minimal, non-driving design;
  continuous risks sliding back toward driving. Undecided.

## How to test (the anti-magic proof)

The only honest test removes the props (memory + human steering + small context):

1. **Fresh-install reproduction.** Fresh profile, **clean memory, minimal pushed
   context**, hand it a real task, and measure: *did it run the gate before its
   first edit?* and *did it reach done through the gates?*
2. **Smallest probe first.** A `Stop` (or `PreToolUse`) hook — claude-CLI-native,
   advising the harness — that, when a fresh agent told only "implement story X"
   tries to edit/finish, asks the satellites client "is X engaged / are its
   mandated items satisfied?" and blocks + reminds on a miss, **with zero process
   pushed into context.** If the chaperone catches the skip at the door on a
   clean install, the thesis holds.
3. **A/B to isolate.** hook on/off, slim/full context — to see what actually
   moves compliance, rather than assuming.
4. Iteration requires **fresh starts (clean memories)** each time, and accepts
   **rebuilds** of the process and implementation.

## What this is NOT (traps to avoid)

- satellites as the harness/driver (replaces claude CLI; V3/V4; slow; hardcoded).
- Feeding the agent one node at a time (satellites-as-driver in disguise).
- Fixing compliance by adding more prose (the doom loop).
- Coupling the enforcement to this repo's pipeline or having CI call the service.
