#!/usr/bin/env bash
# Pushes the current issue branch and opens (or updates) its pull request.
#
# Usage: open-pr.sh <issue-number> [--body-file <path>]
#
# The PR body always ends with "Closes #<issue>" so the merge closes the issue
# automatically; merge-and-close.sh verifies that afterwards regardless.
# Re-running this after new commits just pushes; it never duplicates the PR.

set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

issue="${1:-}"
[ -n "$issue" ] || die "usage: open-pr.sh <issue-number> [--body-file <path>]"
shift

body_file=""
while [ $# -gt 0 ]; do
  case "$1" in
    --body-file) body_file="${2:-}"; shift 2 ;;
    *) die "unknown argument: $1" ;;
  esac
done

require_gh_auth

branch="$(current_branch)"
[ "$(issue_from_branch "$branch")" = "$issue" ] \
  || die "current branch '$branch' does not belong to issue #$issue"

working_tree_is_clean || die "working tree is dirty; commit before opening the PR"

info "pushing $branch"
git -C "$REPO_ROOT" push -u origin "$branch"

existing="$(gh pr list --head "$branch" --state open --json number -q '.[0].number')"
if [ -n "$existing" ]; then
  info "pull request #$existing already open for $branch"
  write_state "$issue" ".pr = $existing"
  echo "$existing"
  exit 0
fi

title="$(gh issue view "$issue" --json title -q '.title')"

tmp_body="$(mktemp)"
if [ -n "$body_file" ]; then
  cat "$body_file" >"$tmp_body"
else
  printf 'Implements issue #%s.\n' "$issue" >"$tmp_body"
fi
printf '\n\nCloses #%s\n' "$issue" >>"$tmp_body"

info "opening pull request"
url="$(gh pr create \
  --base "$DEFAULT_BRANCH" \
  --head "$branch" \
  --title "$title (#$issue)" \
  --body-file "$tmp_body")"
rm -f "$tmp_body"

number="$(gh pr list --head "$branch" --state open --json number -q '.[0].number')"
write_state "$issue" ".pr = $number"
echo "$url" >&2
echo "$number"
