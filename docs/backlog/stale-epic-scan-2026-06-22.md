# Stale-epic scan — 2026-06-22 (satellites-scoped, corrected)

- Scan date: 2026-06-22 (UTC)
- Scope: open anchor stories in THIS repo's project (`proj_fc7d72d8`), status backlog/ready/in-progress
- Run via: `tsk_0c93403e` (satellites-task-workflow); executor = agent

> **Correction.** The first 2026-06-22 run enumerated `document_list` with no
> `project_id` filter; as an admin caller that returned stories from ALL projects,
> so it wrongly proposed cancelling `epic:range-deviation` (proj_6964e0c7, VIRE)
> and `epic:exception-only-reminders` (proj_c6b63a15, timesheet). Those were
> restored, NOT cancelled. The scan now scopes to the repo's project (see the task
> body How step 1). This report is satellites-only.

## Candidates

- `epic:minimal-default` — `sty_015e8dcd` — **CLOSE** — intent (minimal embed /
  de-skill / prune) delivered by the shipped minimal-spine + system-substrate
  work; 5/6 children done, last open child retired so the parent can close.
- `epic:development-mode` — `sty_d87f4719` — **KEEP** — harness feature (per-mode
  substrate bundles); 4 children open, not clearly superseded.
- `epic:repo-substrate` — `sty_2add5e69` — **KEEP** — the live "dogfood the
  process on this repo" umbrella; most recent; 4 children open.

## Summary

3 open epics — 0 cancel / 1 close / 2 keep. Report-only; a human decides each.
