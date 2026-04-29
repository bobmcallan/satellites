#!/usr/bin/env bash
# Local deployment helper for the satellites docker stack. Wraps `docker
# compose` with the repo's compose file + a mandatory .env file so the
# operator invocation stays short. Subcommands: up (default), down, logs,
# restart.
#
# Populate .env first using the env-var reference in README.md
# ("Server configuration" section):
#
#   $EDITOR .env   # set DEV_USERNAME / DEV_PASSWORD / OAuth creds / etc.
#   ./scripts/deploy.sh up
#
# .env is gitignored — treat it as machine-local.
set -euo pipefail

cd "$(dirname "$0")/.."

COMPOSE_FILE="docker/docker-compose.yml"
ENV_FILE=".env"

require_env_file() {
  if [ ! -f "$ENV_FILE" ]; then
    echo "error: $ENV_FILE not found" >&2
    echo "hint:  create $ENV_FILE — see README.md \"Server configuration\" for the env-var reference" >&2
    exit 1
  fi
}

require_compose() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "error: docker is not installed or not in PATH" >&2
    exit 1
  fi
  if ! docker compose version >/dev/null 2>&1; then
    echo "error: 'docker compose' plugin not found (try docker-ce >=20.10 or install the plugin)" >&2
    exit 1
  fi
}

compose() {
  docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"
}

cmd_up() {
  require_env_file
  require_compose
  compose up -d --build
  compose ps
}

cmd_down() {
  require_compose
  compose down
}

cmd_restart() {
  require_env_file
  require_compose
  compose down
  compose up -d --build
  compose ps
}

cmd_logs() {
  require_compose
  compose logs -f --tail=100
}

usage() {
  cat >&2 <<'EOF'
usage: scripts/deploy.sh <command>

Commands:
  up        (default) Build + start the local stack via docker compose.
  down      Stop and remove the stack.
  restart   Down then up.
  logs      Tail combined logs (follow, last 100 lines).

Requires:
  docker    with the compose plugin (`docker compose`).
  .env      create at repo root (gitignored). See README.md
            "Server configuration" for the env-var reference.

The wrapped invocation is:
  docker compose -f docker/docker-compose.yml --env-file .env <subcommand>
EOF
}

main() {
  local sub="${1:-up}"
  case "$sub" in
    up)       cmd_up ;;
    down)     cmd_down ;;
    restart)  cmd_restart ;;
    logs)     cmd_logs ;;
    -h|--help|help) usage; exit 0 ;;
    *)        echo "unknown subcommand: $sub" >&2; usage; exit 2 ;;
  esac
}

main "$@"
