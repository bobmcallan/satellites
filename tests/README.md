# tests/

Test infrastructure for the satellites server.

## Default suite

```
go test ./...
```

Runs every test that does not carry a build tag. Stays green without
external services — testcontainers tests skip on `-short`, live API
tests are excluded by build-tag.

## Live integration tests

A small set of tests exercise real external APIs (Gemini reviewer
today; e2e story lifecycle later). They are gated by a `//go:build
live` constraint so the default suite never invokes them.

### Setup

1. Copy the template:

   ```
   cp tests/.env.example tests/.env
   ```

2. Populate `tests/.env` with at least `GEMINI_API_KEY`. Optional:
   `GEMINI_REVIEW_MODEL`, `EMBEDDINGS_API_KEY`, `EMBEDDINGS_PROVIDER`,
   `EMBEDDINGS_MODEL`. `tests/.env` is gitignored.

3. The loader at `tests/common/dotenv_test_keys.go` runs on package
   init the first time any test imports `tests/common`. Host-exported
   values always win — exporting a key in your shell overrides the
   `tests/.env` value.

### Run

```
make test-live
```

Equivalent to `go test -tags=live -timeout 60s ./internal/reviewer/...`.
Tests skip with `t.Skip` when the relevant credential is missing, so a
partial `tests/.env` only runs the tests it has keys for.

### Opt out

The default `go test ./...` excludes every `//go:build live` file —
contributors without keys see no failure, no skip noise, no extra
runtime.

## TOML by default; ENV is for docker

Production satellites resolves config in the order **process env → TOML
→ in-code defaults**. Integration tests now mirror that path: every
shaped value (port, env, log_level, dev_mode, db_dsn, docs_dir,
oauth_*, api_keys, grants_enforced) lives in a per-test TOML file and
is mounted into the container at `/app/satellites.toml`. Only secrets
(`GEMINI_API_KEY`, `SATELLITES_API_KEYS`) and per-run overrides
(`DB_DSN` pointing at the surreal sibling, `EMBEDDINGS_PROVIDER=stub`,
etc.) flow via the container `Env:` map.

Why: the binary's TOML loader is what PPROD relies on. Tests that
inject everything via ENV never exercise that loader, leaving drift
invisible. The new helper makes TOML the default test-config carrier.

The schema reference is `tests/satellites.example.toml` — it mirrors
the production `satellites.example.toml` so contributors can see at a
glance which keys travel via TOML. (Note: a few env vars —
`EMBEDDINGS_*`, `GEMINI_API_KEY`, `GEMINI_REVIEW_MODEL` — are read
directly via `os.Getenv` in their respective packages and are not
part of `internal/config.Config`. Migrating them into Config is a
follow-up.)

### Helpers

- `tests/integration/toml_boot.go::writeTestTOML(t, cfg)` — serialises
  a `map[string]any` of TOML keys to a per-test file in `t.TempDir()`,
  returns the absolute host path.
- `tests/integration/toml_boot.go::startServerWithTOML(t, ctx, opts,
  tomlPath)` — boots the satellites testcontainer with the TOML
  bind-mounted at `/app/satellites.toml` and `SATELLITES_CONFIG`
  pointing at it. Returns `(baseURL, logs func() string, stop)`.
  The `logs` closure drains the container stdout for boot-log
  assertions; `stop` is the deferred terminate.

### Boot-log evidence

`internal/config.Load()` populates `cfg.LoadedTOMLPath()` with the
path it read; `cmd/satellites/main.go` emits a
`config: loaded TOML path=…` info line at boot. Tests assert on
this log via `logs()` to prove the TOML loader actually ran (not
that the container booted on env+defaults alone).

`TestConfig_ENVOverridesTOML` (`tests/integration/config_resolution_test.go`)
is the lone integration test of the env-only path going forward — it
boots a container with TOML `port=9090` AND env `PORT=8080`, asserts
the container listens on 8080, and asserts the TOML-loaded log line
appears (proving the resolution order rather than env-by-default).

## Layout

- `tests/.env.example` — committed template enumerating the loader's
  whitelisted keys.
- `tests/satellites.example.toml` — schema reference for the
  TOML helper.
- `tests/common/` — shared helpers (dotenv loader, test fixtures).
- `tests/integration/` — testcontainers-driven integration tests.
- `tests/portalui/` — portal UI integration tests.
