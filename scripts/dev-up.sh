#!/usr/bin/env bash
#
# scripts/dev-up.sh — bring up the local satellites stack (Postgres + server).
# satellites-server boots in --dev mode and seeds the admin/user accounts
# with predictable api-keys (sk_dev_admin / sk_dev_user). It applies
# migrations on startup, so no separate migrate step is needed.

set -euo pipefail

cd "$(dirname "$0")/.."

docker compose -f scripts/docker-compose.dev.yml up -d --build

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
