---
name: workflow-authoring
scope: system
headline: How to author a workflow or reviewer skill — the type:skill + kind:<...> frontmatter convention, file homes, and where to find live examples
tags: [area:workflow, kind:reference]
---
# Authoring workflows and reviewer skills

Author process substrate under `.satellites/`, then upload it — each upload runs
the per-type reviewer (that review is the control). Drafting is ungated; the gate
is at upload.

## The one convention that trips people: type + kind

EVERY authored substrate artifact is `type: skill`, differentiated by `kind:`.

| Artifact | Frontmatter | Home | Upload |
| --- | --- | --- | --- |
| Reviewer gate | `type: skill` · `kind: reviewer` | `.satellites/skills/` | `satellites skill upload` |
| Workflow | `type: skill` · `kind: workflow` | `.satellites/workflows/` | `satellites workflow upsert` |
| Capability / function | `type: skill` · `kind: function` | `.satellites/skills/` | `satellites skill upload` |

A `type: workflow` (or any non-`skill` type) under `.satellites/` is rejected with
`type-mismatch`. The type is always `skill`; the **kind** names what it is.

## Reviewer skill frontmatter

    ---
    name: <project>-<object>-<stage>-review
    type: skill
    kind: reviewer
    when: status==<state>        # the status this gate judges
    tags: [kind:reviewer]
    description: <one line — what it judges, and what it enacts on accept>
    ---
    <system-prompt body: the decision rule (accept / reject), what to read, and
    the ledger rows it appends to ENACT the transition on accept>

## Workflow frontmatter

    ---
    name: <name>
    type: skill
    kind: workflow
    applies_to: [<category>, ...]   # the story/task categories it governs
    tags: [kind:workflow]
    description: <one line>
    ---

The body carries a fenced `yaml` block of states + transitions:

    states:
      - <state>
      - {name: <state>, actor: executor}
    transitions:
      - {from: <a>, to: <b>, reviewer_skill: "<gate-name>"}   # a gated edge
      - {from: <b>, to: <c>, trigger: checkpoint}             # ungated executor move

Every `reviewer_skill` and `[[wikilink]]` a workflow names must resolve
(embed → local → server); a dangling reference is refused at upsert.

## Find live examples — do not guess

- `satellites skill get <name>` prints any reviewer's full body, INCLUDING the
  binary-embedded system gates (`satellites-task-upsert-review`,
  `satellites-task-report-review`, and the `satellites-*-review` family). Copy
  their shape.
- `satellites skill list` lists every skill, embedded gates included.
- `satellites workflow show <name>` renders a workflow's states, transitions, and
  the gate on each edge.
- `satellites workflow upsert --dry-run` / `satellites skill upload --dry-run`
  validate without writing.

See [[reviewer-only-model]] for what a reviewer is and the moves it may make.
