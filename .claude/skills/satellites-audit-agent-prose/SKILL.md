<!-- satellites-sync:begin {"document_id":"doc_4dc59149","version":2,"hash":"5dd2d56273ea8f26da0182f87a8e4fd0483daa2eab7f61e9b16ef14bc1e4e9d6"} satellites-sync:end -->
---
name: satellites-audit-agent-prose
type: skill
description: Audit a prose artifact intended for an agent or operator (MCP load instructions, tool descriptions, CLI help, seed/principle markdown, system prompts) for repo-agnostic, short, prescriptive language. Use when the user invokes "/satellites-audit-agent-prose <path>...", says "audit this instruction", "review this prompt", or asks to critique any file that ships text to a downstream reader. Push back hard on narrative, host-repo coupling, and rotting identifiers.
---

# satellites-audit-agent-prose

Prose shipped to an agent or operator is a contract: short, prescriptive, environment-agnostic, actionable on first read. This skill audits one or more files the user names and reports concrete fixes. It does not edit unless asked.

## Invocation

The user passes one or more file paths (typically markdown, but plain text or extracted strings are fine):

```
/satellites-audit-agent-prose path/to/artifact.md [more/paths.md ...]
```

If no path is given, ask which file to audit. Do not guess. Do not walk the tree.

## Procedure

1. **Read each file in full.** Do not skim. If the file is over 500 lines, read it in chunks and audit each.
2. **Run static checks** (below) on the body. Record every violation as `file:line · severity · rule · fix`.
3. **Run the critique pass** (below) on bodies longer than two sentences. Skip for one-line strings unless static checks already flagged them.
4. **Report** in the output format below. Lead with `block` findings, then `warn`, then critique verdicts. No padding, no "looks good" entries.
5. **Stop.** Do not rewrite. If the user wants edits, they will ask.

## Static checks — fail closed

Each hit is a `block` unless noted.

- **Host-repo coupling.** Reject literal phrases that assume the reader is inside the artifact's source repo: `this repo`, `this codebase`, `our codebase`, `our repo`, `in this project`, `here we`. The reader is in *their* environment, not yours.
- **Hardcoded paths under the source repo.** Reject absolute paths (`/home/...`, `/Users/...`, `C:\\...`) and repo-internal source paths (`internal/...`, `cmd/...`, `pkg/...`, `src/...`) unless they appear inside a code fence as an *example* the reader will adapt.
- **Rotting identifiers.** Reject *filled-in* values that rot: UUIDs, hex slugs of the form `[a-z]{2,5}_[0-9a-f]{6,}`, concrete ticket/epic refs (`epic:bootstrap-autonomy`, `JIRA-1234`, `#1234`), commit SHAs, version pins to in-flight builds. *Template* forms with angle-bracket placeholders inside (`epic:<slug>`, `story:<id>`, `project:<id>`) are fine — they document syntax, not state. If an identifier is needed, it must be a placeholder (`<workspace_id>`, `<ticket_id>`).
- **Implementation-status narrative.** Reject `not yet wired`, `tracked under`, `until they land`, `stub until`, `TODO`, `for now`, `coming soon`, `currently`, `in progress`. If a behaviour isn't ready, the artifact must not describe it.
- **Placeholder discipline.** Identifiers the reader supplies must appear in `<angle_brackets>` or `${BRACES}`. Literal example values that look real (e.g., `wsp_4f8c9b2a`) are `warn` — they get copy-pasted by mistake.
- **Prescriptive verbs present.** The artifact body must open with imperative voice — a directive to the reader, not a description of the system. Accept any sentence-initial imperative: `MUST`, `Call`, `Read`, `Write`, `Run`, `Verify`, `Return`, `Pass`, `Reject`, `Complete`, `Treat`, `Resolve`, `Persist`, `Dispatch`, `Skip`, `Compare`, `Parse`, `Use`, `Scan`, `Check`, `Apply`, `Fetch`, `Install`, `Update`, etc. Check the first sentence of the body (skipping frontmatter, title, and any single intro paragraph). If the opening reads as background ("This document describes…", "The system supports…"), flag it.
- **Length budget by surface.**
  - Markdown directive / load instruction: ≤ 150 lines, ≤ 1500 words. `warn` above, `block` at 2×. Multi-step bootstrap artifacts (frontmatter tag `kind:mcp-startup` or similar) get the full 150; single-concern directives should still aim for ≤ 100.
  - Tool / function description string: ≤ 140 characters, single sentence, ends with a period.
  - CLI short help: ≤ 60 characters, imperative mood, no terminal period.
  - CLI long help: ≤ 40 lines.
  - System prompt fragment: ≤ 80 lines per concern.
- **Link / path validity.** Every relative path mentioned outside a code fence must exist on disk (relative to the file's directory or the working dir) or be a documented `<placeholder>`. Dead paths are `block`.
- **Duplicate prose.** If two passages restate the same instruction, one is wrong. `warn` with both line numbers.
- **First-person plural.** `we`, `us`, `our` referring to the authors of the artifact. The reader is the subject. `warn`.

## Critique pass — structured reviewer prompt

For each body that survives static checks, run this critique. Use the Agent tool (subagent_type=general-purpose) and paste the prompt verbatim along with the artifact body. The prompt is the contract.

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

## <next file path>
...

## Summary
- N files audited · N block · N warn · N REVISE/REWRITE
```

## Hard rules — non-negotiable

- **No narrative in a directive.** A directive is a contract, not a design doc. If a sentence doesn't drive an action or state a load-bearing fact, cut it. *Load-bearing facts include*: ordering invariants ("the next step assumes the previous one ran"), ownership claims ("you are the sole writer of X"), error semantics ("treat that error as bootstrap drift"), and authority boundaries ("the CLI never mutates this file"). Keep those. Cut motivation, history, and "why this exists" prose.
- **No live identifiers.** Story IDs, ticket numbers, commit SHAs, dated build versions — all rot. Use `<placeholders>`.
- **No "implementation status" sections.** Either the behaviour ships, or the artifact omits it.
- **No "in our system" framing.** Speak to the reader's environment, not yours.
- **Short over thorough.** When in doubt, cut. Every paragraph in a load-time artifact is a tax on every future session that loads it.

## When to skip a file

- The path points to internal developer docs (ADRs, contributor READMEs, design notes, changelogs, test fixtures). Tell the user the file looks internal and ask whether to audit anyway.
- The path doesn't exist. Surface the missing path; do not search for alternatives.
