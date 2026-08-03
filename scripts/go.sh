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

if ! command -v "${DOCKER}" >/dev/null 2>&1; then
  echo "scripts/go.sh: '${DOCKER}' not found on PATH" >&2
  exit 127
fi

# Who should own the build output?
#
# Rootful Docker (GitHub runners): the container's root is the host's root, so
# running as root leaves root-owned files in dist/ that later non-container
# builds cannot overwrite. Run as the invoking user instead.
#
# Rootless Podman: the container's root already maps to the invoking user, and
# asking for --user maps to an unprivileged subordinate uid that cannot write to
# the mounted workspace at all. Leave the default alone.
user_args=()
if [[ -n "${GO_CONTAINER_USER:-}" ]]; then
  user_args=(--user "${GO_CONTAINER_USER}")
elif ! "${DOCKER}" info 2>/dev/null | grep -qiE 'rootless:[[:space:]]*true|rootless'; then
  user_args=(--user "$(id -u):$(id -g)")
fi

exec "${DOCKER}" run --rm \
  "${user_args[@]}" \
  -v "${repo_root}:/workspace:z" \
  -v "${cache_dir}/build:/gocache:z" \
  -v "${cache_dir}/mod:/gomodcache:z" \
  -e GOCACHE=/gocache \
  -e GOMODCACHE=/gomodcache \
  -e GOFLAGS="${GOFLAGS:-}" \
  -e HOME=/tmp \
  -w "/workspace/${module_dir}" \
  "${GO_IMAGE}" go "$@"
