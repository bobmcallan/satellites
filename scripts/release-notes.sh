#!/usr/bin/env bash
# release-notes.sh — append a CHANGELOG section for the current `[satellites]`
# version using `git log` since the last tagged release. Idempotent: a second
# run for the same version exits 0 without modifying CHANGELOG.md. Story_d69840b1.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

VERSION_FILE=".version"
CHANGELOG_FILE="CHANGELOG.md"

if [[ ! -f "$VERSION_FILE" ]]; then
    echo "release-notes: $VERSION_FILE missing" >&2
    exit 1
fi
if [[ ! -f "$CHANGELOG_FILE" ]]; then
    echo "release-notes: $CHANGELOG_FILE missing" >&2
    exit 1
fi

# Parse the [satellites] section's `version =` value.
VERSION=$(awk '
    /^\[/ { section = $0; next }
    section == "[satellites]" && /^[[:space:]]*version[[:space:]]*=/ {
        gsub(/[[:space:]"]/, "", $3); print $3; exit
    }
' "$VERSION_FILE")

if [[ -z "$VERSION" ]]; then
    echo "release-notes: could not parse [satellites].version from $VERSION_FILE" >&2
    exit 1
fi

ANCHOR="## [$VERSION]"
if grep -q -F "$ANCHOR" "$CHANGELOG_FILE"; then
    echo "release-notes: section $ANCHOR already present — no-op"
    exit 0
fi

# Resolve the commit range: from the most recent tag (if any) to HEAD; else
# from the root commit.
if SINCE=$(git describe --tags --abbrev=0 2>/dev/null); then
    RANGE="${SINCE}..HEAD"
else
    SINCE=$(git rev-list --max-parents=0 HEAD | head -1)
    RANGE="${SINCE}..HEAD"
fi

DATE=$(date -u +"%Y-%m-%d")
TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT

# Build the new section.
{
    echo "## [$VERSION] - $DATE"
    echo
    git log "$RANGE" --reverse --pretty=format:'- %s' || true
    echo
    echo
} > "$TMP"

# Insert after the Unreleased section. The Unreleased anchor is "## [Unreleased]".
awk -v block_file="$TMP" '
    /^## \[Unreleased\]/ { in_unreleased = 1; print; next }
    in_unreleased && /^## \[/ {
        # First version section after Unreleased — splice the new block in front of it.
        while ((getline line < block_file) > 0) print line
        close(block_file)
        in_unreleased = 0
    }
    { print }
    END {
        # CHANGELOG ends inside Unreleased? Append at EOF.
        if (in_unreleased) {
            while ((getline line < block_file) > 0) print line
            close(block_file)
        }
    }
' "$CHANGELOG_FILE" > "$CHANGELOG_FILE.tmp"
mv "$CHANGELOG_FILE.tmp" "$CHANGELOG_FILE"

echo "release-notes: appended section $ANCHOR for range $RANGE"
