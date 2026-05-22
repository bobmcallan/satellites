DATABASE_URL ?= postgres://satellites:satellites@localhost:5432/satellites?sslmode=disable

# Version + build info sourced from .version (canonical) — per binary.
# Each binary is built with its own ldflags so 'satellites version' and
# 'satellites-server' (MCP version verb) report independent values. The
# release workflow uses the same per-binary extraction.
CLI_VERSION    := $(shell awk '$$1=="satellites.version:"        {print $$2}' .version)
CLI_BUILD      := $(shell awk '$$1=="satellites.build:"          {print $$2}' .version)
SERVER_VERSION := $(shell awk '$$1=="satellites-server.version:" {print $$2}' .version)
SERVER_BUILD   := $(shell awk '$$1=="satellites-server.build:"   {print $$2}' .version)
COMMIT         := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

VERB_PKG := github.com/bobmcallan/satellites/internal/verb

CLI_LDFLAGS    := -s -w \
  -X $(VERB_PKG).Version=v$(CLI_VERSION) \
  -X $(VERB_PKG).Commit=$(COMMIT) \
  -X $(VERB_PKG).BuildTime=$(CLI_BUILD)

SERVER_LDFLAGS := -s -w \
  -X $(VERB_PKG).Version=v$(SERVER_VERSION) \
  -X $(VERB_PKG).CLIVersion=v$(CLI_VERSION) \
  -X $(VERB_PKG).Commit=$(COMMIT) \
  -X $(VERB_PKG).BuildTime=$(SERVER_BUILD)

.PHONY: build vet test test-integration migrate-up migrate-down migrate-status version

build:
	go build -trimpath -ldflags="$(CLI_LDFLAGS)"    -o bin/satellites        ./cmd/satellites
	go build -trimpath -ldflags="$(SERVER_LDFLAGS)" -o bin/satellites-server ./cmd/satellites-server

version:
	@echo "satellites        version=$(CLI_VERSION)    build=$(CLI_BUILD)"
	@echo "satellites-server version=$(SERVER_VERSION) build=$(SERVER_BUILD)"
	@echo "commit            $(COMMIT)"

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
