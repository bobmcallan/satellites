---
title: Seed & System Config
slug: seed-and-config
order: 60
tags: [help, seed, config]
---
# Seed & System Config

System-tier configuration (agents, contracts, workflows, help)
is **markdown in the repo**. The boot path reads
`./config/seed/{agents,contracts,workflows}/*.md` and
`./config/help/*.md` and upserts each file into the document
store. Markdown is the single source of truth.

## Layout

```
config/
  seed/
    agents/      *.md  -> type=agent
    contracts/   *.md  -> type=contract
    workflows/   *.md  -> type=workflow
  help/          *.md  -> type=help
```

## Frontmatter

YAML envelope between `---` markers. Required fields per kind:

- `agent`: `name`, `permission_patterns`.
- `contract`: `name`, `category`, `evidence_required`.
- `workflow`: `name`, `required_slots`.
- `help`: `title`, `slug`.

The body is the human description; for help docs the body is
the rendered page itself.

## Re-seed without restart

Global admins can trigger a re-seed without restarting the
server via the **System Config** page in the hamburger menu, or
by calling the `system_seed_run` MCP verb. Each run writes a
`kind:system-seed-run` ledger row with the
`{loaded, created, updated, skipped, errors}` summary so the
audit chain captures every refresh.

## Env overrides

- `SATELLITES_SEED_DIR` — defaults to `./config/seed`.
- `SATELLITES_HELP_DIR` — defaults to `./config/help`.

## Limitations

- The loader is **upsert-only** — files removed from disk do not
  archive their corresponding documents. Removal is a future
  story.
- Idempotence relies on body-hash convergence. Drift in the
  structured payload alone is not detected; if you change only
  frontmatter, also tweak the body (or re-seed twice).
