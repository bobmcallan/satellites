---
name: workflow-patterns
scope: system
headline: The answer sheet for authoring complex workflows — sequential pipelines, bounded review loops, parallel via parent/children, graduated actors, escalation states; yaml is the execution format, DOT is export-only
tags: [area:substrate, kind:reference]
---
# Workflow patterns

A workflow is a fenced `yaml` block of `states` and `transitions` — an
arbitrary digraph. These five patterns cover the shapes teams ask for. The
yaml is the execution format; `workflow show --dot` exports any of them as
Graphviz for visualisation, never the other way round.

A state is a bare name (`- backlog`) or an object
`{name, actor, command}`. `actor` says WHO acts while a story sits there —
an open vocabulary; `executor`, `reviewer`, `satellites`, and `operator`
carry built-in dispatcher semantics. A transition may carry a
`reviewer_skill` gate, an `on: pass|fail` discriminator, a
`max_iterations` bound with its `on_exhausted` landing state, or
`trigger: checkpoint`.

## 1 · Sequential pipeline — more states

When work passes through distinct hands in order, add states rather than
overloading one. Every edge is reviewer-gated; each state is one phase.

```yaml
states: [backlog, designed, built, verified, done]
transitions:
  - {from: backlog,  to: designed, reviewer_skill: "design-review"}
  - {from: designed, to: built,    reviewer_skill: "build-review"}
  - {from: built,    to: verified, reviewer_skill: "test-review"}
  - {from: verified, to: done,     reviewer_skill: "release-review"}
```

## 2 · Exec → review loop with bounds

When a reviewer may send work back, make the review a STATE and the
rejection a real edge. `on: pass|fail` selects the edge from the reviewer's
decision; `max_iterations` + `on_exhausted` bound the loop in code so it can
never spin forever — the Nth failure lands in the escalation state.

```yaml
states:
  - {name: in_progress, actor: executor}
  - {name: review,      actor: reviewer}
  - {name: blocked,     actor: operator}
  - done
transitions:
  - {from: in_progress, to: review, trigger: checkpoint}
  - {from: review, on: pass, to: done}
  - {from: review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: blocked}
```

## 3 · Parallel via parent/children — the anchor is the AND-join

Workflows do not fork; PARALLELISM is structural. Give each concurrent
stream its own child story (each running any workflow it likes), and gate
the parent's single edge on a close review that requires every child
terminal — the anchor is the AND-join.

```yaml
states: [backlog, done]
transitions:
  - {from: backlog, to: done, reviewer_skill: "parent-close-review"}
```

## 4 · Graduated actors — who acts at each step

The same process serves different team mixes: name the actor on each state.
A human dev codes, the platform runs the deterministic test gate (its
`command`'s exit code selects pass/fail — no agent judgement), a reviewer
judges the result. Swap an actor to re-staff the process without changing
its shape.

```yaml
states:
  - {name: coding,    actor: executor}
  - {name: testing,   actor: satellites, command: "make test"}
  - {name: judging,   actor: reviewer}
  - {name: escalated, actor: operator}
  - done
transitions:
  - {from: coding, to: testing, trigger: checkpoint}
  - {from: testing, on: pass, to: judging}
  - {from: testing, on: fail, to: coding, max_iterations: 3, on_exhausted: escalated}
  - {from: judging, on: pass, to: done}
  - {from: judging, on: fail, to: coding, max_iterations: 2, on_exhausted: escalated}
```

## 5 · Escalation states — controlled exhaustion

Every bounded loop names where its Nth failure lands: a state owned by a
human (`actor: operator`). The move into it is enacted by the client, not
decided by the agent; nothing advances out of it until the operator acts.
Declare it like any state — reachable only via `on_exhausted` — and it is
also a terminal rest state until the process is re-armed.

```yaml
states:
  - {name: working,  actor: executor}
  - {name: checking, actor: reviewer}
  - {name: stuck,    actor: operator}
  - done
transitions:
  - {from: working, to: checking, trigger: checkpoint}
  - {from: checking, on: pass, to: done}
  - {from: checking, on: fail, to: working, max_iterations: 2, on_exhausted: stuck}
```

## Choosing

- More phases → pattern 1. A reviewer who can say "do it again" → pattern 2.
- Concurrent streams → pattern 3 (children), never a forked workflow.
- Mixed human/agent/platform teams → pattern 4.
- Any bounded loop → pattern 5 comes with it; never leave exhaustion
  undefined.
