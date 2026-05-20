#!/usr/bin/env bash
#
# scripts/dev-down.sh — tear down the local dev stack (containers + volume).
# WARNING: removes the Postgres data volume — destructive.

set -euo pipefail

cd "$(dirname "$0")/.."

docker compose -f scripts/docker-compose.dev.yml down -v
