#!/usr/bin/env bash
# Prints the next issue that is ready to be implemented.
#
# Ready means: open, not the PRD tracking issue, and every issue listed under
# its "## Blocked by" heading is already closed.
#
# Usage:
#   next-unblocked-issue.sh                 # number of the next ready issue
#   next-unblocked-issue.sh --format json   # full record for the next ready issue
#   next-unblocked-issue.sh --all           # table of every ready issue
#   next-unblocked-issue.sh --include-in-progress
#
# Exit codes: 0 found, 3 nothing ready, 1 usage or tooling error.
#
# Offline testing: --issues-file and --branches-file bypass gh entirely.

set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

SELECTOR="$AGENTLOOP_DIR/select-ready.jq"
format="number"
show_all=0
include_in_progress=0
issues_file=""
branches_file=""

while [ $# -gt 0 ]; do
  case "$1" in
    --format) format="${2:-}"; shift 2 ;;
    --all) show_all=1; shift ;;
    --include-in-progress) include_in_progress=1; shift ;;
    --issues-file) issues_file="${2:-}"; shift 2 ;;
    --branches-file) branches_file="${2:-}"; shift 2 ;;
    -h|--help) sed -n '2,20p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

require_tools jq

if [ -n "$issues_file" ]; then
  issues_json="$(cat "$issues_file")"
else
  require_gh_auth
  issues_json="$(gh issue list --state all --limit 300 \
    --json number,title,url,state,body,labels)"
fi

if [ -n "$branches_file" ]; then
  branches_json="$(cat "$branches_file")"
elif [ -n "$issues_file" ]; then
  branches_json="[]"
else
  branches_json="$(gh pr list --state open --limit 200 --json headRefName \
    -q '[.[].headRefName]')"
fi

ready="$(printf '%s' "$issues_json" \
  | jq --argjson branches "$branches_json" -f "$SELECTOR")"

if [ "$include_in_progress" -eq 0 ]; then
  ready="$(printf '%s' "$ready" | jq '[.[] | select(.has_open_pr | not)]')"
fi

count="$(printf '%s' "$ready" | jq 'length')"
if [ "$count" -eq 0 ]; then
  echo "NONE" >&2
  exit 3
fi

if [ "$show_all" -eq 1 ]; then
  printf '%s' "$ready" | jq -r '.[] | "#\(.number)\t\(.title)"'
  exit 0
fi

case "$format" in
  number) printf '%s' "$ready" | jq -r '.[0].number' ;;
  json)   printf '%s' "$ready" | jq '.[0]' ;;
  line)   printf '%s' "$ready" | jq -r '.[0] | "#\(.number)\t\(.title)"' ;;
  *)      die "unknown --format: $format" ;;
esac
