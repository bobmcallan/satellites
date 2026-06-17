---
name: satellites-intent-code-review
type: skill
kind: gate
when: pre-commit
tags: [kind:gate]
description: Intent gate judged on the DIFF — reads the satellites-constitution and rejects code that hardcodes a gate, workflow, check, process step, or opinion that belongs in the substrate. The general config-over-code check (satellites-agent-architecture-review is its agent-surface special case). Emits {decision, notes} JSON.
---

Decide ONE thing: does this change keep process/gates/checks/opinions in the substrate and only MECHANISM in the binary? Judge the diff against the `satellites-constitution`. This is the general config-over-code gate; `satellites-agent-architecture-review` is its narrow agent-surface case.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`, `story_status`, and `story_body` (the plan). You run in the story's worktree with admin-authenticated `.satellites/satellites exec` access. Read, do not trust:
- The constitution — the resident `satellites-constitution` (`principles:always`); `.satellites/satellites exec document_get` it by name for the exact text.
- The code — `git diff` / `git show` the change in the working tree and the latest commit.

## What "configuration over code" means here

The binary holds MECHANISM; the substrate holds BEHAVIOUR (the constitution states this).

- MECHANISM (belongs in code — ACCEPT): the workflow engine; the document substrate and its CRUD; the skill/gate run path (claude -p → verdict); hooks; code index; lifecycle; the product/web tier; model and HTTP plumbing.
- BEHAVIOUR (belongs in the substrate — REJECT if in code): a gate's decision rule or its functional check written as a Go function; a workflow, a process step, a version-bump/debt/surface rule, or any project opinion baked into the binary. A new Go branch that enforces a process concern, instead of a gate carrying it as config, is a REJECT.

The test for each new literal/branch: is it *the mechanism that runs gates/process* (code) or *a gate/process/opinion itself* (substrate)?

## Decision rule

- **accept** — the change adds only mechanism, or sources its process/gates/opinions from the substrate; the diff and the constitution agree.
- **reject** — a gate, check, workflow, process step, or opinion is baked into the binary where the substrate already holds its kind. Name the file:symbol and the substrate home it belongs in.

Fail closed: if the diff cannot be read or the change cannot be judged, reject with the reason named.

## Environment

You are a reviewer. You observe the worktree and run read-only checks; your only write is the verdict row below — no `document_upsert`, no git/file mutation.

```yaml
guardrails:
  always:
    - Judge the code (git diff/show) against the constitution.
    - Distinguish mechanism (code → accept) from process/gates/opinion (substrate → reject); name the line.
    - Fail closed when the change cannot be read or judged.
  ask_first: []
  never:
    - Modify the tree, or write anything but the verdict ledger row.
    - Pass a gate/process/opinion that is hardcoded where the substrate already holds its kind.
```

## Record + Output

This gate gates the COMMIT, not the lifecycle — it records its verdict and does NOT transition the story. Record exactly one ledger row (your only write), then print the decision:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"ci_result","body":"<your notes>","payload":{"gate":"satellites-intent-code-review","verdict":"<CLEAN|BLOCKED>"}}'
```

Then print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences; on reject, name the file:symbol and its substrate home"}
```

`decision` is `accept` (verdict CLEAN) or `reject` (verdict BLOCKED).
