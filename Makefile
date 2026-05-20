DATABASE_URL ?= postgres://satellites:satellites@localhost:5432/satellites?sslmode=disable

.PHONY: build vet test test-integration migrate-up migrate-down migrate-status

build:
	go build ./cmd/...

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
