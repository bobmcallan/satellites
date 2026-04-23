# satellites-v4

Developer-in-the-loop agentic engineering platform. A server (state + MCP + cron) and a separate worker (satellites-agent) coordinate story implementation against external repos, with humans reviewing every change.

Module path: `github.com/bobmcallan/satellites`.

## Build

Use `scripts/build.sh` for everyday build, lint, and maintenance tasks. It's a plain bash dispatcher — `scripts/build.sh <command>`, with `build` as the default.

```
./scripts/build.sh build     # stamps each binary from its own .version section (default)
./scripts/build.sh server    # builds satellites only  (reads [satellites])
./scripts/build.sh agent     # builds satellites-agent only  (reads [satellites-agent])
./scripts/build.sh fmt       # gofmt -s -w .
./scripts/build.sh vet       # go vet ./...
./scripts/build.sh lint      # golangci-lint run (skipped if not installed)
./scripts/build.sh test      # go test ./...
./scripts/build.sh clean     # remove built binaries
./scripts/build.sh help      # show usage
```

Plain `go build ./...` also works and produces `dev`-stamped binaries with build/commit defaults of `unknown` — suitable for quick iteration without ldflags.

## Deploy locally

The local docker stack (satellites + SurrealDB) is driven by `docker/docker-compose.yml`. Use `scripts/deploy.sh` as the single operator entry point — it wraps `docker compose` with the compose file + a mandatory `.env`.

```
cp .env.example .env       # copy template and edit DEV_USERNAME / DEV_PASSWORD / OAuth creds
./scripts/deploy.sh up     # build + start the stack (default subcommand)
./scripts/deploy.sh logs   # tail combined logs
./scripts/deploy.sh restart
./scripts/deploy.sh down
```

`.env.example` enumerates every env var the server reads (server, auth, OAuth, MCP, documents). `.env` is gitignored — treat it as machine-local.

## Run

```
./satellites         # satellites-server <version> (build: <build>, commit: <commit>)
./satellites-agent   # satellites-agent <version> (build: <build>, commit: <commit>)
```

Each binary prints one boot line with its name and the full version metadata.

## .version

The `.version` file at the repo root carries the semantic version for each binary in its own section. Only `version` is stored — the build timestamp and git commit are generated at build time so they always reflect the actual build moment, not a stale file edit.

```
[satellites]
version = 0.0.1

[satellites-agent]
version = 0.0.1
```

`scripts/build.sh`:
- parses the appropriate section for `version` (section-scoped — never reads across sections),
- generates `build` via `date -u +"%Y-%m-%d-%H-%M-%S"` at build time,
- generates `commit` via `git rev-parse --short HEAD` at build time,
- injects all three into `internal/config.{Version, Build, GitCommit}` via `-ldflags -X`.

Bumping only one section's `version` affects only that binary's boot line version string.

## Version metadata

Runtime exposure lives at `internal/config/version.go`:

```go
var Version   = "dev"     // overridden by ldflags from .version section
var Build     = "unknown" // overridden by ldflags from date -u at build time
var GitCommit = "unknown" // overridden by ldflags from git rev-parse --short HEAD

func GetFullVersion() string  // "<version> (build: <build>, commit: <commit>)"
```

Both `cmd/satellites/main.go` and `cmd/satellites-agent/main.go` call `config.GetFullVersion()` in their boot line. A plain `go build ./...` produces a runnable binary stamped with the three defaults above.
