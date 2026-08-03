#!/usr/bin/env bash
# Assembles the self-review packet for an issue's pull request and enforces the
# review round cap. The script gathers evidence; the agent does the judging.
#
# Usage: review-pr.sh <issue-number> [--max-diff-lines N] [--peek]
#
# --peek prints the packet without consuming a review round.
#
# Exit codes: 0 packet produced, 4 round cap exhausted (escalate instead).

set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

issue="${1:-}"
[ -n "$issue" ] || die "usage: review-pr.sh <issue-number>"
shift

max_diff_lines=1200
peek=0
while [ $# -gt 0 ]; do
  case "$1" in
    --max-diff-lines) max_diff_lines="${2:-}"; shift 2 ;;
    --peek) peek=1; shift ;;
    *) die "unknown argument: $1" ;;
  esac
done

require_gh_auth

branch="$(current_branch)"
[ "$(issue_from_branch "$branch")" = "$issue" ] \
  || die "current branch '$branch' does not belong to issue #$issue"

round="$(read_state "$issue" '.review_rounds' 0)"
if [ "$peek" -eq 0 ]; then
  round=$((round + 1))
  write_state "$issue" ".review_rounds = $round"
fi

echo "=== self-review round $round of $MAX_REVIEW_ROUNDS for issue #$issue ==="

if [ "$round" -gt "$MAX_REVIEW_ROUNDS" ]; then
  echo "round cap exhausted; run escalate.sh instead of another fix cycle" >&2
  exit 4
fi

git -C "$REPO_ROOT" fetch origin "$DEFAULT_BRANCH" >/dev/null 2>&1 || true
base="$(git -C "$REPO_ROOT" merge-base HEAD "origin/$DEFAULT_BRANCH")"

echo
echo "--- acceptance criteria (issue #$issue) ---"
gh issue view "$issue" --json body -q '.body' \
  | sed -n '/^## Acceptance criteria/,/^## /p' \
  | sed '/^## Blocked by/d'

echo
echo "--- commits ---"
git -C "$REPO_ROOT" log --oneline "$base..HEAD"

echo
echo "--- changed files ---"
git -C "$REPO_ROOT" diff --stat "$base..HEAD"

echo
echo "--- diff (first $max_diff_lines lines) ---"
git -C "$REPO_ROOT" diff "$base..HEAD" | head -n "$max_diff_lines"

echo
echo "--- review checklist ---"
cat <<'CHECKLIST'
Blocking findings only. For each, record file, line and the required change.

Correctness and scope
 [ ] Every acceptance checkbox on the issue is genuinely satisfied by this diff
 [ ] Nothing outside the issue's scope was changed
 [ ] No TODO, stub, commented-out code or hardcoded credential left behind

Project constraints (violations are defects, not opinions)
 [ ] Permission precedence lives only in Cerbos policy; no Go code orders
     mandatory deny > user REVOKE > user GRANT > role grant > default deny
 [ ] The ADS assembles permissionContext data only and never computes a verdict
 [ ] No consumer imports a concrete IdP adapter type; selection stays env-driven
 [ ] Liquibase changelogs stay portable across Postgres and Oracle
 [ ] Cerbos policies keep one file per resource

Tests
 [ ] Tests exercise public interfaces and describe behaviour, not structure
 [ ] Tests would survive an internal refactor
 [ ] Each acceptance criterion that can be tested has a test
 [ ] The suite actually ran and passed on this branch

Design
 [ ] Interfaces stayed small; complexity is hidden behind them
 [ ] No duplication introduced that the diff itself already justifies removing
 [ ] Errors are handled and surfaced, not swallowed
CHECKLIST

echo
echo "Optional deeper pass: the graphify skill"
echo "  graphify the changed files against origin/$DEFAULT_BRANCH, then read graphify-out/GRAPH_REPORT.md"
