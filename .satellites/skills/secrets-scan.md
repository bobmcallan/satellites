---
name: secrets-scan
scope: project
type: skill
kind: function
tags: [kind:function]
description: Basic repository secrets scan — sweep tracked files for committed credentials (API keys, private keys, tokens, password assignments, high-entropy strings) and produce a redacted findings report. Use when a task asks to scan the codebase for secrets/credentials. It REPORTS findings (file:line + match kind), never auto-remediates, and NEVER prints or persists a secret's VALUE (only its location and kind).
---

# Secrets scan (basic)

The work-step skill the secrets-check task invokes during its `running` state. **Purpose:** answer "are there credential-shaped strings
committed to tracked files?" and produce a redacted, reviewable report — without
ever exposing a secret's value. It scans **tracked** files only and emits a
**redacted** report; a human acts on the findings.

## Rules

- **Tracked files only** — scan `git ls-files`, never the working tree at large
  (skip `.git`, build output, vendored deps).
- **Report, don't remediate** — list findings; do not edit, rotate, or delete.
- **Never reveal the value** — a finding records `file:line` + the match KIND
  (e.g. "AWS access key id", "private key block"). NEVER copy the matched secret
  text into the report, the task body, or the ledger. Redact to the kind only.
- **Basic by design** — pattern + simple entropy heuristics; false positives are
  expected and noted, not suppressed by cleverness.

## Procedure

Run the pattern sweep over tracked files (each pattern = one match KIND):

```bash
# tracked files only; print file:line:matchkind, never the value
git ls-files -z | xargs -0 grep -nIE \
  -e 'AKIA[0-9A-Z]{16}' \
  -e '-----BEGIN ([A-Z]+ )?PRIVATE KEY-----' \
  -e '(?i)(api[_-]?key|secret|token|passwd|password)[[:space:]]*[:=][[:space:]]*['\''"][^'\''"]{8,}' \
  -e 'gh[pousr]_[A-Za-z0-9]{16,}' \
  -e 'xox[baprs]-[A-Za-z0-9-]{10,}' \
  2>/dev/null | cut -d: -f1,2 | sort -u
```

(Map each hit to its kind by which pattern matched; if unsure, re-run a single
pattern at a time. Treat `.example`/`.sample`/test-fixture files and obvious
placeholders as low-confidence and label them so.)

Optionally flag long high-entropy tokens (≥32 chars of base64/hex assigned to a
variable) as "possible high-entropy secret".

## Report shape

Produce a `## Secrets scan report` section (for the task body) listing, per
finding: `file:line` — kind — confidence (high/low, with a one-word reason such
as "test-fixture" or "placeholder"). End with a one-line verdict:

- `CLEAN — no credentials found in tracked files`, or
- `N finding(s) — see list (values redacted)`.

Never include a secret's value. The report is what the task's
`satellites-task-report-review` gate reads to close the run.

## Verifier

Success is the report itself, judged by the `satellites-task-report-review` gate
(the task's exit gate): the sweep ran over tracked files and a
`## Secrets scan report` with a one-line verdict (CLEAN / N findings) is present,
values redacted. This skill NAMES that reviewer; it does not embed a verdict of
its own.

## Durable dated report file (committed)

Findings must ALSO be written as a durable, versioned deliverable — not only a
ledger/body row — so an operator or auditor can read and diff scans over time.
After the sweep, write a **dated** markdown report and commit it:

1. **Path** — `docs/security/secrets-scan-YYYY-MM-DD.md` (today's UTC date). The
   date stamp makes each run a NEW file; a same-date re-run appends a `-run-N`
   suffix rather than clobbering, so `docs/security/` accumulates the history.

2. **Contents** — plain markdown, viewable, VALUES REDACTED:

   ```markdown
   # Secrets scan — YYYY-MM-DD

   - Scan date: <UTC timestamp>
   - HEAD commit: <git rev-parse --short HEAD>
   - Scope: tracked files (git ls-files)

   ## Findings

   - `path/to/file:line` — <match kind> — <high|low> (<one-word reason>)

   ## Verdict

   CLEAN — no credentials found in tracked files.
   ```

   List one bullet per finding, or state `No findings.` when clean. NEVER write a
   secret's value — location + kind only.

3. **Commit it** — `git add docs/security/secrets-scan-YYYY-MM-DD.md` and commit
   it as part of the work (the report is a versioned artifact, not transient).

4. **Reference it** — name the dated file's path in the task body's
   `## Secrets scan report` section so the report gate sees the durable artifact.

Create `docs/security/` if it does not exist.

## Environment

Acts on the repo: reads tracked files and writes a dated report file. It does not
mutate source or external services.

```yaml
guardrails:
  always:
    - Scan only tracked files (git ls-files); redact every finding to file:line + kind.
    - Write the dated report under docs/security/; name its path in the task body.
  ask_first: []
  never:
    - Print, copy, or persist a secret's VALUE anywhere (report, task body, ledger).
    - Edit, rotate, or delete a credential — this skill REPORTS only.
```
