---
name: story-execution-process
tags: ["principles:project"]
---
# Story execution process

How the agent drives a story to `done`. This is the **first process document**; future process docs follow the same shape (`type:"document"`, `scope:"project"`, kebab-case name, project- or workspace-scoped).

## Per-story rules

### 1. Run the `satellites-commit-push` checkpoint at every natural checkpoint

End of phase, end of meaningful change, before requesting review. Run the
**`satellites-commit-push`** skill (the operator's `/commit-push` slash command
is its interactive shadow — same routine). It bumps `.version` and pushes the release tag, so it is the single point where work becomes visible to other agents, reviewers, and the build pipeline.

Reviewers may run against the latest pushed commit, not your local working tree. Skipping the commit-push checkpoint makes the reviewer judge stale code and is a common cause of false rejections.

### 2. Do not stop unless blocked

Keep driving the story forward — read the workflow skill, do the work, edit the story body, run `satellites story review <id>` (the reviewer gate). On reject, read the notes and iterate.

You are **blocked** only when:

- A reviewer rejection asks for information you cannot infer (a product decision, a credential, an external system reachable only to a human).
- A required tool, verb, or skill is missing or broken in a way you cannot work around.
- The story's workflow skill or project_config is malformed and you do not have permission to fix it.
- A destructive or shared-state action (force-push, deploy, send notification) needs operator confirmation.
- The story carries a `dogfood-after:<story-name>` tag (see below) — that tag is itself a stop signal.

You are **not** blocked when:

- A reviewer rejects on technical grounds you can address. Read the notes, fix, push, request review again.
- A verb returns a transient error. Retry once; on persistent failure, treat as blocked.
- You are unsure of style or convention. Read existing examples, pick the closest match, proceed.

### 3. When blocked, write the reason into the story body and stop

Append a `## Blocked` section to the story body with: what you tried, the precise reason you cannot continue, and what the operator needs to provide or decide. Then stop. Status stays where it is; the operator picks it up.

### 4. Refresh the client after substrate changes — `satellites skill sync`

After a story lands, or after any change to substrate skills (gate or
workflow skills), run `satellites skill sync`. It pulls the current skills
from the substrate into `.claude/skills/<name>/SKILL.md`, pull-only and
reconciled by each materialised skill's identity stamp (it updates or removes
only what it materialised; an operator-authored skill with no stamp is never
touched). Until it runs, the local skill files are stale and the next gate
run executes against old reviewers and workflow — the same stale-source
failure mode that skipping `/commit-push` causes for code.

(Editing a skill is therefore: edit the `config/<wksp>/<proj>/skills/`
source → `satellites skill upload` → `satellites skill sync`. The
`satellites-init` bootstrap wraps this for first-run setup.)

## Tag conventions

### `dogfood-after:<story-name>` — operator-refresh gate

Marks a story that can only be driven through the loop after a named predecessor story has landed AND the operator has refreshed their local environment to pick up the new verbs / load-context / CLI release.

**Agent behaviour on encountering this tag:**

1. Check whether the named predecessor story is `completed`. If not, treat as blocked on the predecessor — append `## Blocked` and stop.
2. If the predecessor is completed: **stop immediately and ask the operator to perform the refresh.** Do not attempt to start the story. Use this exact prompt:

   > Story `<story-name>` is gated on a local refresh. Please:
   > 1. Restart Claude (so the MCP session picks up the new `tools/list` and load-context doc).
   > 2. Run `/satellites-init` to re-bootstrap the CLI (checks for a new release, verifies token, syncs `satellites.toml`).
   >
   > Once both are done, start a new session and I will pick this story back up.

3. Do not proceed in the current session. The MCP `tools/list` and load-context doc are cached at session start; new verbs and instructions land only on reconnect, and continuing would execute against stale substrate.

This pattern applies any time a server-side change requires the agent's session or local CLI to refresh before the next story can be executed against the new substrate.

## Discovering this document

The load-context doc references this process document so any fresh session sees the link. Agents can fetch it directly:

```
document_get scope=project name=story-execution-process
```

Or it arrives in the `principles` sidecar on any story-scope read in this project (see `docs/principle-loading.md`).

## Adding future process documents

Same pattern. Project-scoped `type:"document"`, kebab-case name, tagged `principles:project`. The tag is what causes the substrate to attach it to the sidecar on every relevant call.