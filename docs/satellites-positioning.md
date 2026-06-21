# Satellites — positioning

Extracted from the `epic:workflow-steps` anchor (story:sty_005d3ad7) so the
strategy lives in one durable place and the epic stays focused on delivery.
This is the POC focus to hold.

## What satellites is

A **goal-based layer ON the CLI harnesses** (claude, copilot, gemini) — not a
replacement for them. Satellites adds persistent **story/task** state, grounding,
and **reviewer-gated** quality on top of the harness the developer already runs.
It is a simple add to a repo, augment-not-replace: a gentle on-ramp that moves a
dev team TOWARD AI development alongside their existing tools and people.

We win on **fit** (corporate, Claude-native, story/task + reviewer quality,
adoption on-ramp), not on workflow flexibility.

## The wedge — two Archons, and the gap

Two Archon generations each shipped half of what a real harness needs; the gap
between them is the wedge satellites occupies.

- **v1** (MCP command-center) has a `todo→doing→review→done` board, but **`review`
  is cosmetic and `done` is self-asserted** — no automated verifier, no
  doer≠approver separation, no first-class story.
- **v2** (the "harness builder" pivot) has YAML node-DAG workflows + worktree
  isolation + **human ApprovalNodes**, but **dropped the task / document /
  knowledge model**.

Satellites unifies what neither shipped: **persistent story/task state +
grounding + reviewer-gated repeatable execution** — an automated verifier that
*enacts* (reviewer-only), not a human pause.

## POC objectives (hold these)

1. **A goal-based layer ON the CLI harnesses** — leverage Claude Code
   (Anthropic's millions), don't replace it. Archon REPLACES Claude Code; in a
   corporate setting that won't fly.
2. **Story/task-based** — archon is not; that miss forces extra tooling to make
   it usable. Our story/task separation is where review naturally attaches.
3. **A simple add to a repo** — a developer is up and running immediately.
   (Roadmap: a complete local story DB in markdown.)
4. **Augment, not replace** — a gentle on-ramp, not a rip-and-replace.

Net: **low risk, simple to deploy and understand.**

## Honest caveats

- Archon has more workflow flexibility (DAG / loops) — a deliberate trade for
  simplicity.
- Archon is moving toward a **workflow marketplace** — satellites could too
  (future, not now).
- Archon has ~20k GitHub stars vs satellites' ~2. We compete on **fit + quality**,
  not on reach.

## Roadmap (post-POC, NOT the workflow-steps epic)

- **Server-side harness mode (opt-in):** a future "satellites does the work
  itself" mode runs SERVER-SIDE on an isolated machine via the **LLM API** —
  deliberately NOT client-side `claude -p` multiplication. This is the path to
  archon-like autonomy without re-inventing a client harness or taking on the
  pricing exposure. The retained `internal/agent` (server LLM executor) is its
  seed.
- **Local markdown story DB** — repo-native state.
- **Workflow marketplace.**
- **Minimal `find_*` / `manage_*` MCP surface.**
