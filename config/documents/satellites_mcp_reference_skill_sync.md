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

## Scope: Claude Code skills only

Substrate rows with `type:"skill"` are the only thing this contract materialises. Workflow specs (`feature-workflow.md` and siblings) are `type:"document"` rows read by the server's workflow-skill spec parser; they are not Claude Code skills and must not be written into `.claude/skills/`. `skill list` returns `type:"skill"` rows only, so the source enforces the line; the naming distinction prevents the category error if a reader hand-grafts a workflow spec into the skills tree.

## Failure mode

Skip this step and skills drift between sessions. Reviewer behaviour then varies by whichever client ran last — the failure mode this contract exists to prevent.
