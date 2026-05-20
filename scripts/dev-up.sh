#!/usr/bin/env bash
#
# scripts/dev-up.sh — bring up local Postgres and apply migrations.
#
# satellites-server is run locally on the host against this Postgres; see
# sty_3a7121e6 + sty_9b3e355c for when the server gains a listening surface
# and dev-mode admin/user accounts.

set -euo pipefail

cd "$(dirname "$0")/.."

DATABASE_URL="${DATABASE_URL:-postgres://satellites:satellites@localhost:5432/satellites?sslmode=disable}"
export DATABASE_URL

docker compose -f scripts/docker-compose.dev.yml up -d postgres

echo "Waiting for Postgres..."
for _ in $(seq 1 30); do
    if docker compose -f scripts/docker-compose.dev.yml exec -T postgres \
        pg_isready -U satellites -d satellites >/dev/null 2>&1; then
        break
    fi
    sleep 1
done

echo "Applying migrations..."
make migrate-up

cat <<EOF

Postgres ready at localhost:5432 (db=satellites user=satellites).
DATABASE_URL=$DATABASE_URL

Next:
  go run ./cmd/satellites-server     # listening surface lands with sty_3a7121e6
  go run ./cmd/satellites version
EOF
