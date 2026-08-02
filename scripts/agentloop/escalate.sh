#!/usr/bin/env bash
# Parks an issue that could not be finished within the review round cap.
# Leaves the PR open, records why, labels the issue for a human, and returns
# the repository to the default branch so the loop can pick up the next issue.
#
# Usage: escalate.sh <issue-number> --reason-file <path>

set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

issue="${1:-}"
[ -n "$issue" ] || die "usage: escalate.sh <issue-number> --reason-file <path>"
shift

reason_file=""
while [ $# -gt 0 ]; do
  case "$1" in
    --reason-file) reason_file="${2:-}"; shift 2 ;;
    *) die "unknown argument: $1" ;;
  esac
done
[ -n "$reason_file" ] && [ -f "$reason_file" ] || die "--reason-file is required"

require_gh_auth
ensure_label "$HUMAN_LABEL" "b60205" "Agent loop stopped here; needs a human decision"

pr="$(read_state "$issue" '.pr' '')"
rounds="$(read_state "$issue" '.review_rounds' 0)"

body="$(mktemp)"
{
  printf 'Agent loop stopped after %s self-review rounds.\n\n' "$rounds"
  cat "$reason_file"
  [ -n "$pr" ] && printf '\n\nThe pull request #%s is left open for inspection.\n' "$pr"
} >"$body"

gh issue comment "$issue" --body-file "$body" >/dev/null
gh issue edit "$issue" --add-label "$HUMAN_LABEL" >/dev/null
[ -n "$pr" ] && gh pr comment "$pr" --body-file "$body" >/dev/null
rm -f "$body"

write_state "$issue" ".escalated_at = \"$(date -u +%FT%TZ)\""

if working_tree_is_clean; then
  git -C "$REPO_ROOT" checkout "$DEFAULT_BRANCH" >/dev/null
  git -C "$REPO_ROOT" pull --ff-only origin "$DEFAULT_BRANCH"
else
  echo "warning: working tree is dirty; left on $(current_branch)" >&2
fi

info "issue #$issue escalated and labelled $HUMAN_LABEL"
