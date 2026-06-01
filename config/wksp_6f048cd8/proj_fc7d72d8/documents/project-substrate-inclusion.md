---
name: project-substrate-inclusion
description: The durable rule for what is project-substrate-owned, where it lives, and of what type — so inclusion is read, not re-derived each session.
tags: [principles:project]
---

# Project substrate inclusion rule

What belongs in the project substrate — versus operator-local or
system — is a fixed rule, not a per-session judgement. Read it here;
do not re-derive it. `satellites <noun> upload` enforces the mechanical
part of this rule and refuses to push any source that breaks it.

## Source layout

Project substrate sources are committed under:

```
config/<workspace_id>/<project_id>/{documents,skills,principles}/<name>.md
```

The path carries the identity: scope, `workspace_id`, and
`project_id` come from the directory segments, never from frontmatter.
A workspace-scope source drops the `<project_id>` segment
(`config/<workspace_id>/<kind>/<name>.md`). `config/documents/` is the
system-seed tree — embedded in the server binary, boot-reconciled,
never CLI-uploaded.

## Inclusion rule

Classify every source from its kind directory — no judgement call:

| Kind dir      | Substrate type | Notes                                                           |
| ------------- | -------------- | --------------------------------------------------------------- |
| `skills/`     | `type:skill`   | Project-owned skill. Materialised into `.claude/skills/<name>/SKILL.md` by `satellites skill sync`. |
| `principles/` | `type:document`| Tag `principles:project` so it rides along on story reads.      |
| `documents/`  | `type:document`| Reference / process / config documents.                         |

A file's frontmatter `type:`, when present, must agree with its kind
directory. A `skills/` file may not declare `type:document`; a
`documents/` file may not declare `type:skill`.

### Skills include workflow specs

Every file under `skills/` is a `type:skill` row, materialised the
same way:

- **Reviewer gate skills** — `satellites-<object>-<stage>-review`
  (e.g. `satellites-story-plan-review`).
- **Workflow specs** — `satellites-<type>-workflow` (e.g.
  `satellites-feature-workflow`, `satellites-fix-workflow`). A workflow is
  process, and process in Claude Code
  is a skill. One `SKILL.md` serves two readers: Claude Code indexes
  it by `description`; the gate's workflow parser reads only its fenced
  ```yaml states/transitions block.

Both are skills. There is no "workflows are not skills" carve-out — see
document:satellites_mcp_reference_skill_sync.

### Ownership: the sync stamp is the key, the `satellites-` prefix is the human marker

`satellites skill sync` materialises a skill with an injected identity
stamp (`document_id` / `version` / `hash`, story:sty_4b517016). A skill
in `.claude/skills/` is **project-owned** when it carries that stamp or
has a `config/.../skills/` source. A skill with neither is
operator-authored — out of substrate, never touched by sync or upload.

The stamp is the **reconcile key** (machine identity); the `satellites-`
prefix is the complementary **human identity** — required on the local name
so a reader tells a substrate skill from an operator one at a glance. Two
layers, one fact; they do not compete. See [[satellites-skill-naming]],
[[substrate-taxonomy]], [[reviewer-only-model]].

## Per-type frontmatter conventions

- **Skills** keep their frontmatter in the stored body (the row must be
  a registerable `SKILL.md`); sync adds `version`. The **dispatch contract**
  lets the dynamic index pick a skill from frontmatter without loading bodies:
  - `name` / `description` — identity and the one-line index entry.
  - `kind` — `workflow` | `function` | `gate` | `capability`. Required.
  - `applies_to: [<story-type>...]` — the type binding a `workflow` declares;
    the single source for which workflow a story type uses (it replaces the
    `project-config` `story_types` mapping).
  - `when: <status/guard>` — the point a skill applies (e.g.
    `status==in_progress` for a done gate). Advisory for the index.

  A `workflow` additionally carries the fenced ```yaml states/transitions
  block. `upload` rejects a skill missing `kind`, or a `workflow` missing
  `applies_to`, naming file + rule.
- **Documents and principles** strip frontmatter from the stored body;
  `name` falls back to the filename stem. Principles carry the
  `principles:project` tag.

## What upload enforces

`satellites <noun> upload` validates the whole `config/` tree against
this rule before any dispatch and refuses on violation, naming the file
and the rule. It runs under `--dry-run` as a pure check. It flags:

- a kind-dir / `type:` mismatch;
- a `skills/` file missing required `name` / `description`;
- frontmatter scope / `workspace_id` / `project_id` that disagrees with
  the path;
- drift: a project-owned (stamped) skill in `.claude/skills/` with no
  `config/.../skills/` source.

The agent-prose audit (skill:satellites-audit-agent-prose) is a
separate, advisory pass over agent-facing prose — run it on document
and skill edits; it does not block upload.
