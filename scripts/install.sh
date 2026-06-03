#!/bin/sh
# satellites bootstrap installer (sty_a62ba0c7) — the `curl | sh` first-touch:
#
#   curl -fsSL https://raw.githubusercontent.com/bobmcallan/satellites/main/scripts/install.sh | sh
#
# It resolves the latest release, fetches + sha-verifies the platform binary to
# a temp dir (no pre-existing binary needed), then hands off to
# `satellites install`, which places + re-verifies the binary at its canonical
# location (~/.local/bin/satellites by default; pass --local for in-repo). Any
# extra args are forwarded to `satellites install`.
set -eu

REPO="bobmcallan/satellites"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) echo "satellites install: unsupported arch '$arch'" >&2; exit 1 ;;
esac

tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
	| grep -m1 '"tag_name"' | cut -d'"' -f4)
[ -n "$tag" ] || { echo "satellites install: could not resolve latest release" >&2; exit 1; }

name="satellites-$tag-$os-$arch"
url="https://github.com/$REPO/releases/download/$tag/$name"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "satellites install: fetching $name ..."
curl -fsSL "$url" -o "$tmp/satellites"
curl -fsSL "$url.sha256" -o "$tmp/satellites.sha256"

want=$(cut -d' ' -f1 "$tmp/satellites.sha256")
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/satellites" | cut -d' ' -f1)
else
	got=$(shasum -a 256 "$tmp/satellites" | cut -d' ' -f1)
fi
[ "$want" = "$got" ] || { echo "satellites install: sha256 mismatch (want $want, got $got)" >&2; exit 1; }

chmod +x "$tmp/satellites"
echo "satellites install: placing the binary ..."
exec "$tmp/satellites" install "$@"
