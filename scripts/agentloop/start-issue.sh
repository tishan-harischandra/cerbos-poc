#!/usr/bin/env bash
# Starts work on an issue: verifies a clean tree, syncs the default branch,
# creates the working branch, resets the review state, and prints the issue
# body so the agent has the acceptance criteria in context.
#
# Usage: start-issue.sh <issue-number>

set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

issue="${1:-}"
[ -n "$issue" ] || die "usage: start-issue.sh <issue-number>"
require_gh_auth

working_tree_is_clean \
  || die "working tree is dirty; commit or stash before starting issue #$issue"

meta="$(gh issue view "$issue" --json number,title,state,body,labels)"
state="$(printf '%s' "$meta" | jq -r '.state')"
[ "$state" = "OPEN" ] || die "issue #$issue is $state"

title="$(printf '%s' "$meta" | jq -r '.title')"
branch="$(branch_for_issue "$issue" "$title")"

info "syncing $DEFAULT_BRANCH"
git -C "$REPO_ROOT" checkout "$DEFAULT_BRANCH" >/dev/null
git -C "$REPO_ROOT" pull --ff-only origin "$DEFAULT_BRANCH"

if git -C "$REPO_ROOT" show-ref --verify --quiet "refs/heads/$branch"; then
  info "reusing existing branch $branch"
  git -C "$REPO_ROOT" checkout "$branch" >/dev/null
else
  info "creating branch $branch"
  git -C "$REPO_ROOT" checkout -b "$branch" >/dev/null
fi

write_state "$issue" \
  ".title = \"$(printf '%s' "$title" | sed 's/"/\\"/g')\"
   | .branch = \"$branch\"
   | .review_rounds = 0
   | .started_at = \"$(date -u +%FT%TZ)\""

echo
echo "issue:  #$issue $title"
echo "branch: $branch"
echo "labels: $(printf '%s' "$meta" | jq -r '[.labels[].name] | join(", ")')"
echo
echo "--- issue body ---"
printf '%s\n' "$(printf '%s' "$meta" | jq -r '.body')"
