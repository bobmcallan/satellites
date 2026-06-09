#!/usr/bin/env bash
#
# scripts/record-ci-evidence.sh — record CI stage outcomes (test/release/deploy)
# for a story into the durable QA-evidence trail (sty_aba16a36). The commit-push
# checkpoint already watches the CI chain; this captures each stage's conclusion
# as a `ci_result` ledger row via `satellites evidence ci`, so the audited record
# reflects the deploy, not just the reviewer gates.
#
# Usage: scripts/record-ci-evidence.sh [story-id] [sha]
#   story-id  defaults to the sty_… trailer on HEAD's commit message.
#   sha       defaults to HEAD.
#
# Idempotent and safe to re-run. Uses the local satellites token (satellites.toml)
# and `gh` — no CI secret required.

set -euo pipefail
cd "$(dirname "$0")/.."

SHA="${2:-$(git rev-parse HEAD)}"
STORY="${1:-$(git log -1 --format='%B' "$SHA" 2>/dev/null | grep -oE 'sty_[0-9a-f]+' | tail -1 || true)}"

if [ -z "${STORY:-}" ]; then
    echo "record-ci-evidence: no story id (pass as \$1 or add a sty_… trailer to the commit)" >&2
    exit 1
fi

record_stage() {
    local workflow="$1" stage="$2" concl result
    concl=$(gh run list --commit "$SHA" --workflow "$workflow" \
        --json conclusion --jq '.[0].conclusion // empty' 2>/dev/null || true)
    if [ -z "$concl" ]; then
        echo "  $stage: no run for ${SHA:0:8} — skipped"
        return 0
    fi
    result="failure"
    [ "$concl" = "success" ] && result="success"
    satellites evidence ci "$STORY" --stage "$stage" --result "$result" \
        --ref "$SHA" --notes "gh $workflow conclusion=$concl" >/dev/null
    echo "  $stage: $result (gh: $concl)"
}

echo "recording CI evidence for $STORY @ ${SHA:0:8}"
record_stage test    test
record_stage release release
record_stage deploy  deploy
echo "done — see: satellites evidence show $STORY"
