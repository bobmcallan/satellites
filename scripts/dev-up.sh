#!/usr/bin/env bash
#
# scripts/dev-up.sh — bring up the local satellites stack (Postgres + server).
# satellites-server boots in --dev mode and seeds the admin/user accounts
# with predictable api-keys (sk_dev_admin / sk_dev_user). It applies
# migrations on startup, so no separate migrate step is needed.

set -euo pipefail

cd "$(dirname "$0")/.."

# Bootstrap scripts/satellites-server.toml from the checked-in example
# if missing. The container bind-mounts only this file (read-only,
# gitignored) — no broader access to the repo. Fill in [oauth] there
# to surface provider buttons on /login.
if [ ! -f scripts/satellites-server.toml ]; then
    echo "scripts/satellites-server.toml not found — copying from example."
    cp scripts/satellites-server.toml.example scripts/satellites-server.toml
    echo "Edit scripts/satellites-server.toml [oauth] section, then re-run scripts/dev-up.sh"
    echo "to enable GitHub / Google sign-in buttons (file is gitignored)."
fi

# --force-recreate ensures Docker re-stages the single-file bind mount
# after host-side edits to scripts/satellites-server.toml. Without it,
# Docker Desktop on WSL2 holds onto the old inode and host edits don't
# appear inside the container until the next compose down/up.
docker compose -f scripts/docker-compose.dev.yml up -d --build --force-recreate

echo "Waiting for satellites-server..."
for _ in $(seq 1 60); do
    if curl -fsS -o /dev/null http://localhost:8080/mcp \
        -H "Authorization: Bearer sk_dev_admin" \
        -X POST \
        -d '{}' 2>/dev/null; then
        break
    fi
    if curl -fsS -o /dev/null -w '%{http_code}' http://localhost:8080/mcp 2>/dev/null | grep -q '^4'; then
        # 401 / 400 — server is up and responding, just rejecting our probe
        break
    fi
    sleep 1
done

cat <<EOF

Satellites dev stack ready:
  Postgres : localhost:5432 (db=satellites user=satellites)
  Server   : http://localhost:8080
  MCP path : http://localhost:8080/mcp
  Dev keys : sk_dev_admin (RoleAdmin) | sk_dev_user (RoleUser)

Smoke test:
  curl -X POST http://localhost:8080/mcp \\
       -H 'Authorization: Bearer sk_dev_admin' \\
       -d '{}'  # MCP rejects sessionless requests by spec; 4xx means up
EOF
