#!/usr/bin/env bash
# Run the Go toolchain inside a container so no Go install is needed on the host.
#
#   scripts/go.sh <module-dir> <go-args...>
#
# Example: scripts/go.sh apps/ads test ./...
set -euo pipefail

GO_IMAGE="${GO_IMAGE:-docker.io/library/golang:1.23-alpine}"
DOCKER="${DOCKER:-docker}"

if [[ $# -lt 2 ]]; then
  echo "usage: scripts/go.sh <module-dir> <go-args...>" >&2
  exit 64
fi

module_dir="$1"
shift

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cache_dir="${repo_root}/.gocache"
mkdir -p "${cache_dir}/build" "${cache_dir}/mod"

if command -v "${DOCKER}" >/dev/null 2>&1; then
  exec "${DOCKER}" run --rm \
    -v "${repo_root}:/workspace:z" \
    -v "${cache_dir}/build:/gocache:z" \
    -v "${cache_dir}/mod:/gomodcache:z" \
    -e GOCACHE=/gocache \
    -e GOMODCACHE=/gomodcache \
    -e GOFLAGS="${GOFLAGS:-}" \
    -w "/workspace/${module_dir}" \
    "${GO_IMAGE}" go "$@"
fi

echo "scripts/go.sh: '${DOCKER}' not found on PATH" >&2
exit 127
