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
make build           # ldflags-injects version/commit/buildTime from .version
make version         # report current version/commit/build
```

`.version` (repo root) is the canonical version source. `/commit-push` bumps it on every commit; the release workflow reads it to derive the release tag.

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

The local-dev stack lives under `scripts/`. One command brings up Postgres + `satellites-server` (in `--dev` mode with pre-seeded `admin` / `user` accounts):

```sh
./scripts/dev-up.sh        # docker compose up; satellites-server builds first time
./scripts/dev-logs.sh      # tail container logs
./scripts/dev-reset.sh     # drop + recreate satellites DB, restart server
./scripts/dev-down.sh      # tear down (removes data volume — destructive)
```

Once up:

- Postgres: `localhost:5432` (db=satellites user=satellites)
- Server:   `http://localhost:8080`, MCP at `/mcp`
- Dev keys: `sk_dev_admin` (RoleAdmin), `sk_dev_user` (RoleUser)

```sh
curl -X POST http://localhost:8080/mcp \
     -H 'Authorization: Bearer sk_dev_admin' -d '{}'
```
