#!/usr/bin/env bash
# One-screen progress report for the agent loop.
#
# Usage: status.sh

set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

require_gh_auth

issues="$(gh issue list --state all --limit 300 --json number,title,state,labels)"

total="$(printf '%s' "$issues" | jq '[.[] | select(([.labels[]?.name] | index("prd")) == null)] | length')"
closed="$(printf '%s' "$issues" | jq '[.[] | select(([.labels[]?.name] | index("prd")) == null) | select(.state == "CLOSED")] | length')"
parked="$(printf '%s' "$issues" | jq --arg l "$HUMAN_LABEL" '[.[] | select(.state == "OPEN") | select([.labels[]?.name] | index($l))] | length')"

echo "slices: $closed of $total closed, $parked parked for a human"
echo
echo "open pull requests:"
gh pr list --state open --json number,title,headRefName \
  -q '.[] | "  #\(.number)\t\(.title)"' || true
echo
echo "ready to start:"
"$AGENTLOOP_DIR/next-unblocked-issue.sh" --all 2>/dev/null | sed 's/^/  /' \
  || echo "  none"
