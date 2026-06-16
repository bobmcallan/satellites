---
name: satellites-agent-architecture-review
type: skill
kind: gate
when: pre-commit
tags: [kind:gate]
description: "Pre-commit gate for agent/executor changes — critiques the plan and code for configuration over code. Agent BEHAVIOUR (which tools, the operating prompt, the process/policy) must live in the substrate (skill frontmatter, system documents, principles), not baked into the binary; MECHANISM (the loop, the tool catalogue, security guardrails) is accepted. Emits a {decision, notes} JSON verdict."
---
<!-- satellites-sync:begin {"document_id":"doc_c19bf434","version":3,"hash":"0f4af1fac8ab872064a84c85a3b5fa3a88d441a9f983451be2b44c39dfe8a1cf","publisher":"proj_682cfeed"} satellites-sync:end -->
<!-- satellites-library:begin {"publisher":"proj_682cfeed","repo":"https://github.com/bobmcallan/satellites-skills","commit":"7caa10cbeb50ac1856b1576e7ffbdafc7ca746eb"} satellites-library:end -->

Decide whether an agent/executor change keeps BEHAVIOUR in configuration and only MECHANISM in code — the project's primary objective. Run when a change touches the agent surface (internal/agent, the agent executor in internal/verb, or agent operating documents).

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`, `story_status`, and `story_body` (the plan). You run in the story's worktree with admin-authenticated `.satellites/satellites exec` access. Read, do not trust:
- The plan (`story_body`) — what the change intends.
- The code — `git diff` / `git show` the agent-area change in the working tree and the latest commit.

## What "configuration over code" means here

The binary holds MECHANISM; the substrate holds BEHAVIOUR.

- MECHANISM (belongs in code — ACCEPT): the agent loop; the tool CATALOGUE that binds capability names to the verb registry and carries their wire schemas; security guardrails (e.g. forcing the workspace scoping into every tool call); the model/HTTP plumbing.
- BEHAVIOUR (belongs in the substrate — REJECT if in code): WHICH tools a task may use (declare in the kind:task skill frontmatter `tools`); the agent operating/system prompt (a system-scope document); any process or policy the agent follows (skills, principles, workflows). A hardcoded tool allowlist, a system/agent prompt as a Go string literal, or a task's process written into Go is a REJECT.

The test for each new literal: is it *what a capability is* (mechanism, code) or *which capabilities/prompt/process this task uses* (behaviour, substrate)?

## Decision rule

- **accept** — new agent behaviour is sourced from configuration; only mechanism is in code; the plan and the diff agree.
- **reject** — agent behaviour (a tool allowlist, an operating prompt, a process/policy) is baked into the binary where its kind already lives in the substrate. Name the file:symbol and the substrate home it belongs in.

Fail closed: if you cannot read the diff or judge the change, reject with the reason named.

## Environment

You are a reviewer. You observe the worktree and run read-only checks; your only write is the verdict row below — no `document_upsert`, no git/file mutation.

```yaml
guardrails:
  always:
    - Judge BOTH the plan (story_body) and the code (git diff/show) of the agent-area change.
    - Distinguish mechanism (code → accept) from behaviour/policy (substrate → reject); name the line.
    - Fail closed when the change cannot be read or judged.
  ask_first: []
  never:
    - Modify the tree, or write anything but the verdict ledger row.
    - Pass agent behaviour that is hardcoded where the substrate already holds its kind (tools, prompts, process).
```

## Record + Output

This gate gates the COMMIT, not the lifecycle — it records its verdict and does NOT transition the story. Record exactly one ledger row (your only write), then print the decision:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"ci_result","body":"<your notes>","payload":{"gate":"satellites-agent-architecture-review","verdict":"<CLEAN|BLOCKED>"}}'
```

Then print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences; on reject, name the file:symbol and its substrate home"}
```

`decision` is `accept` (verdict CLEAN) or `reject` (verdict BLOCKED).
