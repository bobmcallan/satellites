#!/usr/bin/env bash
#
# scripts/dev-logs.sh — tail logs from the local dev stack.
# Pass service names as args to filter (e.g. ./scripts/dev-logs.sh postgres).

set -euo pipefail

cd "$(dirname "$0")/.."

docker compose -f scripts/docker-compose.dev.yml logs -f "$@"
