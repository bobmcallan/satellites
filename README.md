# Satellites V5

**Authored-process substrate that attaches to whatever primary interface (Claude Code, Warp, Codex, Gemini CLI) — turns agent orchestrators into governed workflows.**

V5 is a clean-slate rewrite. Predecessors: [v3](https://github.com/bobmcallan/satellites-v3) (in-session, no audit) and [v4](https://github.com/bobmcallan/satellites-v4) (dispatch-via-`claude -p`, content-coupled).

See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the architectural commitment — four primitives (story, tools, review, evidence), framework-not-definition discipline, parallel-reviewer loop, thin MCP + CLI gateway, `.satellites/` colocated state.

## Status

Pre-implementation. `epic:v5-foundation` is the active backlog (tracked in the V4-hosted substrate while V5 itself is being built).

## Repository layout

```
cmd/satellites-server/   # server binary entrypoint
cmd/satellites/          # CLI binary entrypoint
internal/                # private packages
migrations/              # Postgres migrations
tests/integration/       # Go + testcontainers integration tests
scripts/                 # local dev (docker-compose) + helpers
docs/                    # architecture + design records
.github/workflows/       # CI + release pipelines
```

## Building

```sh
go build ./cmd/...
```

## Local development

`scripts/` will host docker-compose for a local stack once `sty_829b05b4` lands. For now, the binaries are stubs that compile and print a placeholder.
