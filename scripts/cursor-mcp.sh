#!/usr/bin/env bash
# Configure Cursor MCP for satellites from .satellites/satellites.toml.
#
# With [auth].token: writes native Streamable HTTP config (recommended).
# Without token: writes stdio + mcp-remote for the OAuth browser flow.
#
# Usage:
#   ./scripts/cursor-mcp.sh          # write .cursor/mcp.json
#   ./scripts/cursor-mcp.sh --run    # stdio bridge (legacy; Cursor calls this)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TOML="${SATELLITES_CONFIG:-$ROOT/.satellites/satellites.toml}"
SERVER_URL="${SATELLITES_SERVER_URL:-https://satellites-pprod.fly.dev}"
TOKEN=""
MCP_JSON="$ROOT/.cursor/mcp.json"

toml_value() {
	local key="$1"
	[[ -f "$TOML" ]] || return 0
	grep -E "^${key}[[:space:]]*=" "$TOML" 2>/dev/null | head -1 \
		| sed -E 's/^[^=]*=[[:space:]]*"([^"]*)".*/\1/' || true
}

if [[ -f "$TOML" ]]; then
	_url="$(toml_value server_url)"
	_tok="$(toml_value token)"
	[[ -n "$_url" ]] && SERVER_URL="$_url"
	[[ -n "$_tok" ]] && TOKEN="$_tok"
fi

MCP_URL="${SERVER_URL%/}/mcp"

run_stdio_bridge() {
	local args=(-y mcp-remote "$MCP_URL")
	if [[ -n "$TOKEN" ]]; then
		args+=(--header "Authorization:Bearer ${TOKEN}")
	fi
	exec npx "${args[@]}"
}

write_config() {
	mkdir -p "$(dirname "$MCP_JSON")"
	if [[ -n "$TOKEN" ]]; then
		cat >"$MCP_JSON" <<EOF
{
  "mcpServers": {
    "satellites": {
      "url": "${MCP_URL}",
      "headers": {
        "Authorization": "Bearer ${TOKEN}"
      }
    }
  }
}
EOF
		echo "Wrote ${MCP_JSON} (Streamable HTTP + bearer token from ${TOML})."
		echo "Restart Cursor, or disable and re-enable the satellites MCP server."
	else
		cat >"$MCP_JSON" <<EOF
{
  "mcpServers": {
    "satellites": {
      "type": "stdio",
      "command": "bash",
      "args": ["\${workspaceFolder}/scripts/cursor-mcp.sh", "--run"]
    }
  }
}
EOF
		echo "Wrote ${MCP_JSON} (stdio OAuth bridge; no token in ${TOML})."
		echo "Restart Cursor, complete OAuth in the browser, then re-run this script."
	fi
}

case "${1:-}" in
--run)
	run_stdio_bridge
	;;
"")
	write_config
	;;
*)
	echo "Usage: $0 [--run]" >&2
	exit 2
	;;
esac
