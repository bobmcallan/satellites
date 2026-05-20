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
make build           # or: go build ./cmd/...
```

## Database & migrations

Schema lives under `internal/db/migrations/` (golang-migrate, embedded into the binary). The migrator runs against any Postgres reachable via `DATABASE_URL`:

```sh
export DATABASE_URL=postgres://satellites:satellites@localhost:5432/satellites?sslmode=disable
make migrate-up       # apply all migrations
make migrate-status   # current version
make migrate-down     # roll back all migrations (dev only)
```

`make migrate-*` shells out to `go run github.com/golang-migrate/migrate/v4/cmd/migrate` — no external tool needed. The same migrations are embedded into `satellites-server` for boot-time application and into the integration test harness.

## Local development

The local-dev stack lives under `scripts/`. Until `satellites-server` gains a listening HTTP surface (`sty_3a7121e6`) and dev-mode users (`sty_9b3e355c`), the stack brings up Postgres only — the server binary runs on the host against the compose-managed Postgres.

```sh
./scripts/dev-up.sh        # docker compose up postgres + make migrate-up
./scripts/dev-logs.sh      # tail container logs
./scripts/dev-reset.sh     # drop + recreate satellites DB, re-apply migrations
./scripts/dev-down.sh      # tear down (removes data volume — destructive)
```

Then in another shell:

```sh
export DATABASE_URL=postgres://satellites:satellites@localhost:5432/satellites?sslmode=disable
go run ./cmd/satellites-server     # stub for now (listening surface arrives with sty_3a7121e6)
go run ./cmd/satellites version
```
