---
name: satellites_mcp_reference_skill_sync
scope: system
tags: [kind:mcp-reference]
---
# satellites · reference: Claude Code skill sync

How the agent reconciles substrate `type:"skill"` rows into the local `.claude/skills/` tree on session start. The load-context bootstrap step names this contract; the detail lives here so the orientation payload stays within the host's reminder cap.

## Contract

For each applicable scope (system; workspace with `--scope workspace --workspace <wksp_id>`; project with `--scope project --workspace <wksp_id> --project <proj_id>`), run `satellites skill list`. For each returned row:

1. Read `.claude/skills/<name>/SKILL.md`. Missing file, missing `version:` in frontmatter, or `version` below the substrate `latest_version` → outdated.
2. For each outdated row, call `satellites skill get <name>` (same scope flags) and write the body to `.claude/skills/<name>/SKILL.md` with frontmatter `name`, `description`, and `version` set to the synced `latest_version`.
3. Pull-only. Never delete a local skill file — operators own `.claude/skills/`, and a local skill with no substrate counterpart is left alone.

## The stored body is a clean SKILL.md; identity is injected locally

The substrate row for a `type:"skill"` is the **authored** SKILL.md — `name`, `description`, the body — and nothing more. `satellites skill upload` preserves that frontmatter in the stored body (documents strip theirs); the row is therefore directly registerable as a skill. The substrate never stores sync bookkeeping.

When the client **materialises** `.claude/skills/<name>/SKILL.md` it injects a sync-identity block that lives only on disk, never in the substrate:

- `document_id` — the source row id. A rename-stable pointer from the local file back to its source, so a reconcile keys on identity, not name.
- `version` — the source `document_version` this copy was cut from. Cheap staleness check: substrate `latest_version` > stamped `version` ⇒ an update is available, no hashing needed.
- `hash` — content hash of the **authored** body, computed with the injected block excluded. To detect a local edit the client strips its own block, re-hashes the on-disk body, and compares to the stamped `hash`; a mismatch means the operator edited the file ⇒ do not overwrite.

Injection is client-side at materialise time. Storing the block in the substrate would make a body contain its own hash (circular) and break the upload round-trip, so it stays local. These three keys are sufficient for a client to decide create / update / skip / refuse-overwrite from the local file plus the document row alone; the non-clobber reconcile that uses them is the materialise/deploy path, not this read contract.

## Scope: every `type:"skill"` row — including workflow skills

Substrate rows with `type:"skill"` are what this contract materialises, and that now **includes workflow skills** (`feature-workflow`, `fix-workflow`, …). A workflow is process, and process in Claude Code is a skill: each is a `type:"skill"` `<name>/SKILL.md` carrying skill frontmatter (`name`, `description`, `version`) for indexing/sync plus the workflow-only `applies_to` and a fenced ```yaml states/transitions block. `skill list` returns them like any other skill, and the reconcile materialises them into `.claude/skills/<name>/SKILL.md`.

One artifact, two readers, no fork: Claude Code indexes the skill by its `description`, and the gate's workflow parser reads only the fenced yaml block (ignoring the prose), so the same SKILL.md serves both. `project-config` points `workflow_skill` at `.claude/skills/<name>/SKILL.md` — the materialised path — so the gate runs the same file the operator authored and synced.

(History: workflow specs were once `type:"document"` flat files excluded from `skill list` and forbidden in `.claude/skills/`. sty_cce5abc0 reversed that — they are first-class skills now.)

## Failure mode

Skip this step and skills drift between sessions. Reviewer behaviour then varies by whichever client ran last — the failure mode this contract exists to prevent.
