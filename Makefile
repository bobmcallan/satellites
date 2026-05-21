DATABASE_URL ?= postgres://satellites:satellites@localhost:5432/satellites?sslmode=disable

# Version + build info sourced from .version (canonical) — auto-bumped on
# every commit by the /commit-push skill. Local builds inject these via
# ldflags so 'satellites version' reports them; the release pipeline reads
# the same file to derive the git tag.
VERSION := $(shell awk '/^version:/ {print $$2}' .version)
BUILD   := $(shell awk '/^build:/ {print $$2}' .version)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

LDFLAGS := -s -w \
  -X github.com/bobmcallan/satellites/internal/verb.Version=v$(VERSION) \
  -X github.com/bobmcallan/satellites/internal/verb.Commit=$(COMMIT) \
  -X github.com/bobmcallan/satellites/internal/verb.BuildTime=$(BUILD)

.PHONY: build vet test test-integration migrate-up migrate-down migrate-status version

build:
	go build -trimpath -ldflags="$(LDFLAGS)" ./cmd/...

version:
	@echo "version=$(VERSION) commit=$(COMMIT) build=$(BUILD)"

vet:
	go vet ./...

test:
	go test ./...

test-integration:
	go test -tags=integration ./tests/integration/...

migrate-up:
	go run -mod=mod github.com/golang-migrate/migrate/v4/cmd/migrate \
		-path internal/db/migrations \
		-database "$(DATABASE_URL)" up

migrate-down:
	go run -mod=mod github.com/golang-migrate/migrate/v4/cmd/migrate \
		-path internal/db/migrations \
		-database "$(DATABASE_URL)" down

migrate-status:
	go run -mod=mod github.com/golang-migrate/migrate/v4/cmd/migrate \
		-path internal/db/migrations \
		-database "$(DATABASE_URL)" version
