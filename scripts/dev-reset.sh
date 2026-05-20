#!/usr/bin/env bash
#
# scripts/dev-reset.sh — reset Postgres data without rebuilding the image.
# Drops the satellites DB, recreates it, re-applies migrations.

set -euo pipefail

cd "$(dirname "$0")/.."

DATABASE_URL="${DATABASE_URL:-postgres://satellites:satellites@localhost:5432/satellites?sslmode=disable}"
export DATABASE_URL

echo "Stopping satellites-server (drops connections to satellites DB)..."
docker compose -f scripts/docker-compose.dev.yml stop satellites-server >/dev/null

docker compose -f scripts/docker-compose.dev.yml exec -T postgres \
    psql -U satellites -d postgres -c "DROP DATABASE IF EXISTS satellites;"

docker compose -f scripts/docker-compose.dev.yml exec -T postgres \
    psql -U satellites -d postgres -c "CREATE DATABASE satellites;"

echo "Restarting satellites-server (will reapply migrations + reseed dev users)..."
docker compose -f scripts/docker-compose.dev.yml start satellites-server

echo "DB reset complete."
