#!/usr/bin/env bash
#
# scripts/dev-reset.sh — reset Postgres data without rebuilding the image.
# Drops the satellites DB, recreates it, re-applies migrations.

set -euo pipefail

cd "$(dirname "$0")/.."

DATABASE_URL="${DATABASE_URL:-postgres://satellites:satellites@localhost:5432/satellites?sslmode=disable}"
export DATABASE_URL

docker compose -f scripts/docker-compose.dev.yml exec -T postgres \
    psql -U satellites -d postgres -c "DROP DATABASE IF EXISTS satellites;"

docker compose -f scripts/docker-compose.dev.yml exec -T postgres \
    psql -U satellites -d postgres -c "CREATE DATABASE satellites;"

make migrate-up

echo "DB reset complete."
