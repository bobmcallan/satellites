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

## What "active engagement" means (sty_2b6cd041)

The door no longer trusts `engagement.json` presence. It reads the engagement
**store** (`.satellites/state.db`, `internal/workstate`) keyed by the
PreToolUse **`session_id`**, and treats an engagement as **active** only when, for
that session, it is:

1. present (an `engage` row, not a bare `candidate` from a story access),
2. **lease-fresh** (`lease_until` is in the future), AND
3. in an **editable phase** — `work init` records, at engage time, whether the
   story's current status is editable per its `## Workflow`
   (`internal/workflow.IsEditable`); the door reads that stored flag (fast, no
   per-edit workflow fetch). Editable is **derived from the workflow**, never
   hard-coded; an unresolvable workflow defaults editable so the lease stays the
   hard gate.

A stale (expired-lease) engagement, a non-editable phase (e.g. backlog/done), a
candidate row, or no engagement for the session → **deny**. This closes the VIRE
bug, where a 9.5h-expired-lease engagement admitted ~20 edits. `engagement.json`
is kept as a transitional artifact but is no longer the door's authority.

## Gate decision

| State for the editing session (in the store) | Door |
| ------------------------------------------- | ---- |
| repo not configured (no `satellites.toml`) / store unreadable | **deny** — fail closed |
| no engagement for the session               | **deny** — run `satellites work init <story>` |
| engagement present but lease expired        | **deny** — re-engage (`satellites work init`) |
| engagement lease-fresh but non-editable phase | **deny** — transition to an editable status |
| engagement lease-fresh AND editable          | **allow** (tool proceeds through normal permissioning) |

A `deny` is emitted as a Claude Code `PreToolUse` decision
(`permissionDecision: "deny"`); an `allow` emits nothing, so the door only ever
blocks — it never auto-approves an edit.
