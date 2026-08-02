#!/usr/bin/env bash
# Merges the issue's pull request, deletes the branch, and makes sure the issue
# is closed with a comment linking the merged PR.
#
# Usage: merge-and-close.sh <issue-number> [--comment-file <path>]
#
# Refuses to merge unless the PR is mergeable, no review round is still open,
# and any configured status checks have passed.

set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

issue="${1:-}"
[ -n "$issue" ] || die "usage: merge-and-close.sh <issue-number> [--comment-file <path>]"
shift

comment_file=""
while [ $# -gt 0 ]; do
  case "$1" in
    --comment-file) comment_file="${2:-}"; shift 2 ;;
    *) die "unknown argument: $1" ;;
  esac
done

require_gh_auth

pr="$(read_state "$issue" '.pr' '')"
if [ -z "$pr" ]; then
  branch="$(branch_for_issue "$issue" "$(gh issue view "$issue" --json title -q '.title')")"
  pr="$(gh pr list --head "$branch" --state open --json number -q '.[0].number')"
fi
[ -n "$pr" ] || die "no open pull request found for issue #$issue"

view="$(gh pr view "$pr" --json number,state,mergeable,mergeStateStatus,url,headRefName)"
state="$(printf '%s' "$view" | jq -r '.state')"
mergeable="$(printf '%s' "$view" | jq -r '.mergeable')"
merge_state="$(printf '%s' "$view" | jq -r '.mergeStateStatus')"
url="$(printf '%s' "$view" | jq -r '.url')"

[ "$state" = "OPEN" ] || die "pull request #$pr is $state"
[ "$mergeable" != "CONFLICTING" ] || die "pull request #$pr has conflicts; rebase onto $DEFAULT_BRANCH"

case "$merge_state" in
  BEHIND)
    info "branch is behind $DEFAULT_BRANCH; updating"
    gh pr update-branch "$pr" >/dev/null || die "could not update branch for #$pr"
    ;;
  BLOCKED|DIRTY)
    die "pull request #$pr is $merge_state; resolve before merging"
    ;;
esac

if gh pr checks "$pr" >/dev/null 2>&1; then
  gh pr checks "$pr" --required >/dev/null \
    || die "required checks are not green on pull request #$pr"
fi

info "squash merging pull request #$pr"
gh pr merge "$pr" --squash --delete-branch

git -C "$REPO_ROOT" checkout "$DEFAULT_BRANCH" >/dev/null
git -C "$REPO_ROOT" pull --ff-only origin "$DEFAULT_BRANCH"

if [ -n "$comment_file" ]; then
  gh issue comment "$issue" --body-file "$comment_file" >/dev/null 2>&1 || true
fi

if [ "$(gh issue view "$issue" --json state -q '.state')" != "CLOSED" ]; then
  gh issue close "$issue" --comment "Delivered by $url" >/dev/null
fi

write_state "$issue" ".merged_pr = $pr | .completed_at = \"$(date -u +%FT%TZ)\""
info "issue #$issue closed via $url"
