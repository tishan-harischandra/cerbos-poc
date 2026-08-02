#!/usr/bin/env bash
# Behaviour tests for the agent loop's issue selection logic.
# Runs entirely offline against JSON fixtures; never touches GitHub.
#
# Usage: scripts/agentloop/tests/run-tests.sh

set -uo pipefail

TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOP_DIR="$(dirname "$TESTS_DIR")"
FIXTURES="$TESTS_DIR/fixtures"
NEXT="$LOOP_DIR/next-unblocked-issue.sh"

passed=0
failed=0
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

check() {
  local name="$1" expected="$2" actual="$3"
  if [ "$expected" = "$actual" ]; then
    printf 'ok   %s\n' "$name"
    passed=$((passed + 1))
  else
    printf 'FAIL %s\n     expected: %s\n     actual:   %s\n' "$name" "$expected" "$actual"
    failed=$((failed + 1))
  fi
}

next_with() {
  "$NEXT" --issues-file "$1" "${@:2}" 2>/dev/null
}

# --- selection behaviour -----------------------------------------------------

check "picks the lowest-numbered issue whose blockers are all closed" \
  "3" \
  "$(next_with "$FIXTURES/issues.json")"

check "an issue with an open blocker is not offered" \
  "3
5" \
  "$(next_with "$FIXTURES/issues.json" --all | sed -E 's/^#([0-9]+).*/\1/')"

check "the PRD tracking issue is never offered" \
  "" \
  "$(next_with "$FIXTURES/issues.json" --all | grep -c '^#1	' | sed 's/^0$//')"

check "references outside the Blocked by heading are not treated as blockers" \
  "true" \
  "$(next_with "$FIXTURES/issues.json" --all | grep -q '^#5	' && echo true || echo false)"

# --- in-progress handling ----------------------------------------------------

printf '%s\n' '["issue-3-first-real-authorization-decision"]' >"$tmp/branches.json"

check "an issue with an open pull request is skipped by default" \
  "5" \
  "$(next_with "$FIXTURES/issues.json" --branches-file "$tmp/branches.json")"

check "--include-in-progress still reports the in-flight issue" \
  "3" \
  "$(next_with "$FIXTURES/issues.json" --branches-file "$tmp/branches.json" --include-in-progress)"

# --- terminal condition ------------------------------------------------------

jq 'map(.state = "CLOSED")' "$FIXTURES/issues.json" >"$tmp/all-closed.json"
next_with "$tmp/all-closed.json" >/dev/null
check "exits 3 when every slice is closed" "3" "$?"

# --- branch naming -----------------------------------------------------------

# shellcheck source=../lib.sh
source "$LOOP_DIR/lib.sh"
set +e

check "branch names are derived from the issue number and a slug" \
  "issue-7-role-permission-resolution" \
  "$(branch_for_issue 7 "Role permission resolution")"

check "branch slugs collapse punctuation and case" \
  "issue-12-k6-load-suite-at-1-000-virtual-users" \
  "$(branch_for_issue 12 "k6 load suite at 1,000 virtual users")"

check "the issue number round-trips out of a branch name" \
  "12" \
  "$(issue_from_branch "issue-12-k6-load-suite")"

echo
echo "$passed passed, $failed failed"
[ "$failed" -eq 0 ]
