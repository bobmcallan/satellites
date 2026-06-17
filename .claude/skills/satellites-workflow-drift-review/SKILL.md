---
name: satellites-workflow-drift-review
type: skill
kind: gate
when: pre-commit
tags: [kind:gate, content-review:allow-refs]
description: The process-drift pre-commit gate. Run before every commit that touches process configuration (workflow/gate/capability skills, principles, story workflows) — `satellites workflow check` reconciles the defined process against the executable reality and fails closed on any blocking drift (shadow gates, unusable skills, host-coupled system scope, ungoverned stories, degenerate workflows, shallow first gates, gate-placement conflicts).
---
<!-- satellites-sync:begin {"document_id":"doc_73f36afc","version":5,"hash":"5901f0aeb286007cc48e5d6508bb4c9ca8eb2729a56a2a66c3f4a87084598fb4","publisher":"proj_682cfeed"} satellites-sync:end -->
<!-- satellites-library:begin {"publisher":"proj_682cfeed","repo":"git@github.com:bobmcallan/satellites-skills.git","commit":"02290b00e3a68ffca0360fb69f90f61e83fee8bb"} satellites-library:end -->

# satellites-workflow-drift-review

Run before the commit step of every checkpoint that touches process
configuration — a workflow, gate, or capability skill; a principle; a story's
embedded workflow — or any time you want to know the defined process and the
executable reality still agree.

## Spec — the decision rule

```bash
satellites workflow check
```

**Exit 0 (CLEAN)** — no blocking drift; the commit may proceed. Advisory
findings (`nonatomic-candidate`) report without blocking — follow their
pointer when one appears.

**Exit 1 (BLOCKED)** — a blocking finding; **do not commit**. Each finding
names its artifact and the repair: name the orphaned gate in a workflow
definition (or retire it), materialise or correct a missing reviewer, repair
the unusable SKILL.md (re-run `satellites skill sync`), re-scope or rewrite a
host-coupled system skill, embed a `## Workflow` in (or cover the category
of) an ungoverned story, deepen a shallow entry gate, remove a
`gate-placement-conflict` (a non-gate skill restating a command the workflow
binds to a state — reference the gate by `[[name]]` instead). Fix the drift or
correct the definition, then re-run.

## Environment

Read-only over the materialised skill tree and the project's stories; it
writes nothing.

```yaml
guardrails:
  always:
    - Fail closed — exit 1 blocks the commit.
  ask_first: []
  never:
    - Commit while the check reports a blocking finding.
    - Silence a finding by deleting the artifact it names without an operator decision.
```
