.PHONY: test test-live test-e2e

# Default test suite — fast, no network. Build-tag-gated tests are
# excluded; testcontainer tests skip on -short.
test:
	go test -short ./...

# Live integration tests for external services (Gemini reviewer, etc.)
# gated by `//go:build live`. Requires GEMINI_API_KEY in tests/.env or
# host env. Empty key is a hard fail (no PASS-by-skip). The 60s timeout
# matches the per-call HTTP timeout in internal/reviewer/gemini.go.
test-live:
	go test -tags=live -timeout 60s ./internal/reviewer/...

# End-to-end story lifecycle test against testcontainers, asserting the
# in-container reviewer wires Gemini (not AcceptAll). Requires
# GEMINI_API_KEY in tests/.env or host env. Empty key fails the
# SATELLITES_E2E_REQUIRE_GEMINI=1 enforcement check.
test-e2e:
	SATELLITES_E2E_REQUIRE_GEMINI=1 go test -count=1 -timeout 300s -run TestE2E_StoryLifecycle_FullFlow ./tests/integration/...
