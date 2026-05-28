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

## Scope: Claude Code skills only

Substrate rows with `type:"skill"` are the only thing this contract materialises. Workflow specs (`feature-workflow.md` and siblings) are `type:"document"` rows read by the server's workflow-skill spec parser; they are not Claude Code skills and must not be written into `.claude/skills/`. `skill list` returns `type:"skill"` rows only, so the source enforces the line; the naming distinction prevents the category error if a reader hand-grafts a workflow spec into the skills tree.

## Failure mode

Skip this step and skills drift between sessions. Reviewer behaviour then varies by whichever client ran last — the failure mode this contract exists to prevent.
