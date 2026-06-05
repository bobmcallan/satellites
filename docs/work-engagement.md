# `.satellites/work` — the engagement read-contract

`.satellites/work/` is **local, per-worktree working state** (gitignored, never
committed). It is how the satellites client and the harness hooks communicate
about *what the agent is currently working on* — see
`docs/satellites-rethink.md` (§ "Full-context retrieval") and
`docs/agent-process-compliance.md` (the doors model).

This document pins the **read-contract** the START-door hook
(`satellites hook gate`, epic:hook-enforcement) depends on. The hook only ever
**reads** this file; the **writer** (`satellites work init <story>`) is a
separate story.

## File

```
<work_dir>/engagement.json        # default: <repo>/.satellites/work/engagement.json
```

`<repo>` is the directory that holds `.satellites/satellites.toml` — the hook
walks up from the tool event's `cwd` to find it. `<work_dir>` defaults to
`<repo>/.satellites/work` and is overridable via `satellites.toml`'s `work_dir`
(repo-relative or absolute); the key need not appear in the toml. Reader (the
hook) and writer (`satellites work init`) resolve it through the **same**
`cliconfig.ResolveWorkDir`, so they always agree.

## Shape

```json
{
  "story_id": "sty_<hex>",
  "status":   "in_progress"
}
```

| Field      | Required | Meaning                                                            |
| ---------- | -------- | ----------------------------------------------------------------- |
| `story_id` | yes      | The story whose workflow is engaged in this worktree.             |
| `status`   | no       | The story's current workflow status. Advisory for the door today. |

Unknown fields are ignored, so the writer may carry more (timestamps, gate
evidence) without breaking the reader.

## What "active engagement" means (today)

The START door treats the engagement as **active** when the file:

1. exists at the path above,
2. parses as JSON, and
3. names a story (`story_id` is non-empty).

That is the whole assessment — a **presence check**. The door does **not** read
the workflow, judge work, or check the `status` value yet. Richer,
workflow-position guards (e.g. "edits permitted only once the story has passed
its plan gate") are a later story and will be **derived from the workflow
skill**, not hard-coded — keeping configuration-over-code intact.

## Gate decision

| State of `.satellites/work/engagement.json` | Door |
| ------------------------------------------- | ---- |
| repo not configured (no `satellites.toml`)  | **deny** — fail closed; run `satellites init` |
| configured, no active engagement            | **deny** — run `satellites work init <story>` |
| configured, active engagement               | **allow** (tool proceeds through normal permissioning) |

A `deny` is emitted as a Claude Code `PreToolUse` decision
(`permissionDecision: "deny"`); an `allow` emits nothing, so the door only ever
blocks — it never auto-approves an edit.
