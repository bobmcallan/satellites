---
name: substrate-taxonomy
scope: project
tags: [area:substrate]
---
# Substrate taxonomy: skill / principle / document

Three kinds of substrate artifact. Place each by a one-line test, so placement
is never a per-session judgement. This is the *placement test*;
`project-substrate-inclusion` owns the file layout and the upload mechanics.

## The one-line test

- **Skill — _follow_.** A process or function the agent runs. Carries a yaml
  dispatch definition — a workflow's states/transitions, or a gate's
  role + rubric. If the artifact is something you *do*, it is a skill.
- **Principle — _obey_.** An always-on constraint that holds regardless of the
  task (configuration-over-code; reviewer authority). If the artifact is a rule
  you must not break, it is a principle.
- **Document — _consult_.** Reference read on demand: process notes, schemas,
  and repo/project **intent** (the why / north-star). Guidance — not a step,
  not a constraint. If the artifact is something you *look up*, it is a
  document.

## Where each lives

| Kind | Source | Delivered as |
|---|---|---|
| Skill | `.satellites/skills/<name>.md` (system: `config/documents/`) | `.claude/skills/satellites-<name>/SKILL.md`, synced |
| Principle | `.satellites/principles/<name>.md` (system: `config/documents/`) | rides along on reads via the `principles:*` tag |
| Document | `.satellites/documents/<name>.md` (system: `config/documents/`) | fetched on demand: `document_get name=<name>` |

Ride-along is a **tag** (`principles:*`), not a kind: a document may carry it to
ride along, but a document's default is consult-on-demand.

## Visibility: levels + consumption (order-4)

Every artifact DECLARES a **level** — one vocabulary across all kinds — that
resolves to a storage scope:

| Level | Scope | Meaning |
|---|---|---|
| `system` | system | built-in baseline; always present; never user-published |
| `project` | project | this project's own, by membership |
| `global` | library | shared, published for cross-repo consumption |

**Publish routes by level** (`skill publish`): a `global` artifact lands on the
shared library surface (publisher-stamped), a `project` artifact stays
project-scoped, `system` is refused. **Sync pulls by entitlement**: system
always; project/workspace by membership; **global only when the repo OPTS IN via
`global_publishers`** (the publishers whose global artifacts it consumes). This
RETIRES the per-skill `library_pins` (a remaining `library_pins` is honoured as a
deprecated fallback — its publishers are derived).

A **workflow is the local/project customization unit**: it lives in
`.satellites/workflows/` (client-dir config, order-2), is resolved locally, and
is NEVER consumed as a global skill — global consumption skips `kind:workflow`
even if a publisher hosts one. A repo composes externally-developed **gates**
(consumed `global`) under its own **workflow** (local).

## Intent is a Document

Repo/project **intent** — why the project exists, what "good" means here — is a
Document. It informs judgement; it does not gate a transition or prescribe a
step, so it is neither a principle nor a skill. See `project-intent` for the
worked example.

See [[project-substrate-inclusion]], [[reviewer-only-model]].
