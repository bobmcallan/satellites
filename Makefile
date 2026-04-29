.PHONY: test test-live

# Default test suite — fast, no network. Build-tag-gated tests are
# excluded.
test:
	go test ./...

# Live integration tests for external services (Gemini reviewer, etc.)
# gated by `//go:build live`. Requires GEMINI_API_KEY in the
# environment or tests/.env. The 60s timeout matches the per-call HTTP
# timeout in internal/reviewer/gemini.go.
test-live:
	go test -tags=live -timeout 60s ./internal/reviewer/...
