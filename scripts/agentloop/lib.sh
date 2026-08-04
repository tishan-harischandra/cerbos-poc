#!/usr/bin/env bash
# Shared helpers for the agent loop scripts.
# Source this file; do not execute it directly.

set -euo pipefail

AGENTLOOP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(git -C "$AGENTLOOP_DIR" rev-parse --show-toplevel)"
STATE_DIR="$REPO_ROOT/.agentloop"
DEFAULT_BRANCH="${AGENTLOOP_DEFAULT_BRANCH:-main}"
MAX_REVIEW_ROUNDS="${AGENTLOOP_MAX_REVIEW_ROUNDS:-8}"
HUMAN_LABEL="needs-human"

die() {
  echo "error: $*" >&2
  exit 1
}

info() {
  echo "==> $*"
}

require_tools() {
  local tool
  for tool in "$@"; do
    command -v "$tool" >/dev/null 2>&1 || die "required tool not found on PATH: $tool"
  done
}

require_gh_auth() {
  require_tools gh jq
  gh auth status >/dev/null 2>&1 || die "gh is not authenticated; run 'gh auth login'"
}

# slugify <text> -> lowercase, alnum and dashes only, max 50 chars
slugify() {
  printf '%s' "$1" \
    | tr '[:upper:]' '[:lower:]' \
    | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//' \
    | cut -c1-50 \
    | sed -E 's/-+$//'
}

branch_for_issue() {
  local number="$1" title="$2"
  printf 'issue-%s-%s' "$number" "$(slugify "$title")"
}

# Returns the issue number encoded in a branch name, or empty.
issue_from_branch() {
  printf '%s' "$1" | sed -nE 's/^issue-([0-9]+)-.*/\1/p'
}

state_file_for_issue() {
  printf '%s/issue-%s.json' "$STATE_DIR" "$1"
}

ensure_state_dir() {
  mkdir -p "$STATE_DIR"
}

# read_state <issue> <jq-path> <default>
read_state() {
  local file
  file="$(state_file_for_issue "$1")"
  [ -f "$file" ] || { printf '%s' "$3"; return 0; }
  jq -r --arg d "$3" "($2) // \$d" "$file"
}

# write_state <issue> <jq-assignment-expression>
write_state() {
  local issue="$1" expr="$2" file tmp
  ensure_state_dir
  file="$(state_file_for_issue "$issue")"
  [ -f "$file" ] || printf '{"issue":%s}\n' "$issue" >"$file"
  tmp="$(mktemp)"
  jq "$expr" "$file" >"$tmp"
  mv "$tmp" "$file"
}

working_tree_is_clean() {
  [ -z "$(git -C "$REPO_ROOT" status --porcelain)" ]
}

current_branch() {
  git -C "$REPO_ROOT" rev-parse --abbrev-ref HEAD
}

# Ensures a label exists so scripts never fail on a missing label.
ensure_label() {
  local name="$1" color="$2" description="$3"
  if ! gh label list --limit 200 --json name -q '.[].name' | grep -qx "$name"; then
    gh label create "$name" --color "$color" --description "$description" >/dev/null
  fi
}
