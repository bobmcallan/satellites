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

## Layout

- `tests/.env.example` — committed template enumerating the loader's
  whitelisted keys.
- `tests/common/` — shared helpers (dotenv loader, test fixtures).
- `tests/integration/` — testcontainers-driven integration tests.
- `tests/portalui/` — portal UI integration tests.
