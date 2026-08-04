#!/usr/bin/env bash
# Runs the Cerbos CLI in a container so no host install is needed.
#
# Usage: scripts/cerbos.sh <cerbos-subcommand> [args...]
#
# Example: scripts/cerbos.sh compile /policies
set -euo pipefail

DOCKER="${DOCKER:-docker}"
CERBOS_IMAGE="${CERBOS_IMAGE:-ghcr.io/cerbos/cerbos:0.46.0}"

if ! command -v "${DOCKER}" >/dev/null 2>&1; then
  echo "scripts/cerbos.sh: '${DOCKER}' not found on PATH" >&2
  exit 127
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

exec "${DOCKER}" run --rm \
  --entrypoint /cerbos \
  -v "${repo_root}/deploy/cerbos/policies:/policies:ro,z" \
  "${CERBOS_IMAGE}" "$@"
