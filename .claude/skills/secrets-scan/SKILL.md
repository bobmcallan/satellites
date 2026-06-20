---
description: Basic repository secrets scan — sweep tracked files for committed credentials (API keys, private keys, tokens, password assignments, high-entropy strings) and produce a redacted findings report. Use when a task asks to scan the codebase for secrets/credentials. The initial implementation is deliberately BASIC — it REPORTS findings (file:line + match kind), it never auto-remediates, and it NEVER prints or persists a secret's value (only its location and kind).
---

# Secrets scan (basic)

The work-step skill the secrets-check task (epic:project-tasks) invokes during
its `running` state. It scans **tracked** files only and emits a **redacted**
report.

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
