---
name: satellites-audit-agent-prose
type: skill
kind: capability
tags: [kind:capability, content-review:allow-refs]
description: Audit a prose artifact intended for an agent or operator (MCP load instructions, tool descriptions, CLI help, seed/principle markdown, system prompts) for repo-agnostic, short, prescriptive language. Use when the user invokes "/satellites-audit-agent-prose <path>...", says "audit this instruction", "review this prompt", or asks to critique any file that ships text to a downstream reader. Push back hard on narrative, host-repo coupling, and rotting identifiers.
---
<!-- satellites-sync:begin {"document_id":"doc_745199cd","version":1,"hash":"32a5e61e3527c02e42b5c17ad5afad65d36c154acba0d6e4dee9e839229fe915","publisher":"proj_682cfeed"} satellites-sync:end -->
<!-- satellites-library:begin {"publisher":"proj_682cfeed","repo":"git@github.com:bobmcallan/satellites-skills.git","commit":"45628d3a97a4328fdd77aa83cb11ec77ee432dd0"} satellites-library:end -->

# satellites-audit-agent-prose

Audit one or more prose files the user names and report concrete fixes. Do not
edit unless asked.

## Invocation

```
/satellites-audit-agent-prose path/to/artifact.md [more/paths.md ...]
```

If no path is given, ask which file to audit. Do not guess. Do not walk the tree.

## Procedure

1. **Read each file in full.** If over 500 lines, read in chunks and audit each.
2. **Run static checks** (below) on the body. Record every violation as
   `file:line · severity · rule · fix`.
3. **Run the critique pass** (below) on bodies longer than two sentences. Skip for
   one-line strings unless static checks flagged them.
4. **Report** in the output format below. Lead with `block`, then `warn`, then
   critique verdicts. No "looks good" entries.
5. **Stop.** Do not rewrite.

## Static checks — fail closed

Each hit is a `block` unless noted.

- **Host-repo coupling.** Reject phrases that assume the reader is inside the
  artifact's source repo: `this repo`, `this codebase`, `our codebase`, `our repo`,
  `in this project`, `here we`.
- **Hardcoded paths under the source repo.** Reject absolute paths (`/home/...`,
  `/Users/...`, `C:\\...`) and repo-internal source paths (`internal/...`,
  `cmd/...`, `pkg/...`, `src/...`) unless inside a code fence as an example to adapt.
- **Rotting identifiers.** Reject filled-in values that rot: UUIDs, hex slugs of
  the form `[a-z]{2,5}_[0-9a-f]{6,}`, concrete ticket/epic refs
  (`epic:bootstrap-autonomy`, `JIRA-1234`, `#1234`), commit SHAs, version pins to
  in-flight builds. *Template* forms with angle-bracket placeholders inside
  (`epic:<slug>`, `story:<id>`, `project:<id>`) are fine. If an identifier is
  needed it must be a placeholder (`<workspace_id>`, `<ticket_id>`).
- **Implementation-status narrative.** Reject `not yet wired`, `tracked under`,
  `until they land`, `stub until`, `TODO`, `for now`, `coming soon`, `currently`,
  `in progress`. If a behaviour isn't ready, the artifact must not describe it.
- **Placeholder discipline.** Reader-supplied identifiers must appear in
  `<angle_brackets>` or `${BRACES}`. Literal example values that look real (e.g.
  `wsp_4f8c9b2a`) are `warn`.
- **Prescriptive verbs present.** The body must open with imperative voice — a
  directive, not a description (`MUST`, `Call`, `Read`, `Run`, `Verify`, `Return`,
  `Reject`, `Use`, `Check`, etc.). Check the first sentence of the body (skipping
  frontmatter, title, and any single intro paragraph). If it reads as background
  ("This document describes…", "The system supports…"), flag it.
- **Length budget by surface.**
  - Markdown directive / load instruction: ≤ 150 lines, ≤ 1500 words. `warn` above,
    `block` at 2×. Single-concern directives should aim for ≤ 100.
  - Tool / function description string: ≤ 140 characters, single sentence, ends
    with a period.
  - CLI short help: ≤ 60 characters, imperative mood, no terminal period.
  - CLI long help: ≤ 40 lines.
  - System prompt fragment: ≤ 80 lines per concern.
- **Link / path validity.** Every relative path outside a code fence must exist on
  disk (relative to the file's dir or working dir) or be a `<placeholder>`. Dead
  paths are `block`.
- **Duplicate prose.** Two passages restating the same instruction → `warn` with
  both line numbers.
- **First-person plural.** `we`, `us`, `our` referring to the artifact's authors —
  the reader is the subject. `warn`.

## Critique pass — structured reviewer prompt

For each body that survives static checks, run this critique. Use the Agent tool
(subagent_type=general-purpose) and paste this prompt verbatim with the artifact
body:

```
You are reviewing one prose artifact that will be shipped to a
downstream agent or operator — they will read it without your
context, your repo, or your conversation. Score it against four rules.
For each rule answer PASS / WARN / FAIL with one sentence of evidence
quoting the text.

1. Environment-agnostic. Could this artifact ship unchanged to a
   reader who has never seen the source repository? Flag every
   sentence that references the author's own source tree,
   identifiers, internal tickets, or in-flight work.

2. Prescriptive. Every sentence either tells the reader what to do,
   or states a load-bearing fact the reader needs to act. Flag
   narrative, background, motivation, justification, or
   "why this exists" prose.

3. Unambiguous on first read. If the reader had only this text and
   the surrounding tool / command surface, could they act without
   guessing? Flag every place a reader would have to ask "what does
   that mean" or "where does X come from".

4. Minimum viable length. Could 20% of the words be cut without
   losing intent? Flag redundancy, throat-clearing, restated
   instructions, scene-setting paragraphs.

Output: a table of (rule, verdict, evidence, fix). End with a
one-line overall verdict: SHIP / REVISE / REWRITE.
```

Record each artifact's overall verdict in the report.

## Output format

```
# satellites-audit-agent-prose report

## <file path>
- L<line> · block · <rule> · <fix>
- L<line> · warn  · <rule> · <fix>
- critique: REVISE — <one-line summary of critique fixes>

## Summary
- N files audited · N block · N warn · N REVISE/REWRITE
```

## Hard rules

- **No narrative in a directive.** If a sentence doesn't drive an action or state a
  load-bearing fact, cut it. Load-bearing facts to KEEP: ordering invariants,
  ownership claims, error semantics, authority boundaries. Cut motivation, history,
  "why this exists".
- **No live identifiers** — use `<placeholders>`.
- **No "implementation status" sections** — either the behaviour ships, or omit it.
- **No "in our system" framing** — speak to the reader's environment.
- **Short over thorough.** When in doubt, cut.

## When to skip a file

- Internal developer docs (ADRs, contributor READMEs, design notes, changelogs,
  test fixtures) — tell the user the file looks internal and ask whether to audit anyway.
- A path that doesn't exist — surface the missing path; do not search for alternatives.
