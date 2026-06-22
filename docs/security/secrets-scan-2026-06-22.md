# Secrets scan — 2026-06-22

- Scan date: 2026-06-22 (UTC)
- HEAD: 9ff669a (uncommitted working tree)
- Scope: tracked files (`git ls-files`)
- Run via: `tsk_7e0f74d4` (satellites-task-workflow); executor = agent + `.claude/skills/secrets-scan`
- **Values redacted — only `file:line` + match kind recorded.**

## Methodology note (skill bug found)

The skill's sweep uses `grep -E -e '(?i)…'`, but GNU `grep -E` does not honour the
PCRE `(?i)` inline flag — so the case-insensitive assignment pattern **silently
under-reports** (a first `-E` sweep returned 0). This scan used `grep -P` for the
assignment pattern to get correct case-insensitive matching. The literal patterns
(AWS `AKIA…`, `BEGIN … PRIVATE KEY`, `ghp_…`, `xox…`) are case-sensitive and work
under `-E`. **Recommend fixing the skill** to use `-P` (or `grep -i`).

## Findings

Credential-shaped assignments (`api-key|secret|token|passwd|password = "…"`),
all in TEST files (fixtures) — LOW confidence, values redacted:

- `internal/cli/cmd_auth_test.go:69` — credential-shaped assignment — LOW (test-fixture)
- `internal/cli/dispatch_test.go:34` — credential-shaped assignment — LOW (test-fixture)
- `internal/cliconfig/cliconfig_test.go:96` — credential-shaped assignment — LOW (test-fixture)
- `internal/cliconfig/cliconfig_test.go:137` — credential-shaped assignment — LOW (test-fixture)
- `internal/cliconfig/cliconfig_test.go:168` — credential-shaped assignment — LOW (test-fixture)
- `internal/cliconfig/cliconfig_test.go:308` — credential-shaped assignment — LOW (test-fixture)
- `internal/cliconfig/credstore_test.go:15` — credential-shaped assignment — LOW (test-fixture)
- `tests/integration/oauth_test.go:52` — credential-shaped assignment — LOW (test-fixture)

No AWS access keys, no private-key blocks, no GitHub (`ghp_…`) or Slack (`xox…`)
tokens found in tracked files.

## Verdict

8 LOW-confidence finding(s) — all test fixtures (redacted); **no real credentials
detected** in tracked files.
