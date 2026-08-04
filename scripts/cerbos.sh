#!/usr/bin/env bash
# Runs the Cerbos CLI in a container so no host install is needed.
#
# Usage: scripts/cerbos.sh <cerbos-subcommand> [args...]
#
# The directory mounted at /policies defaults to the bundle the running PDP
# serves. POLICY_DIR selects another one, which is how the ADR-003 control
# experiment is compiled without shipping it to the PDP.
#
# Example: scripts/cerbos.sh compile /policies
#          POLICY_DIR=deploy/cerbos/control scripts/cerbos.sh compile /policies
set -euo pipefail

DOCKER="${DOCKER:-docker}"
CERBOS_IMAGE="${CERBOS_IMAGE:-ghcr.io/cerbos/cerbos:0.46.0}"
POLICY_DIR="${POLICY_DIR:-deploy/cerbos/policies}"

if ! command -v "${DOCKER}" >/dev/null 2>&1; then
  echo "scripts/cerbos.sh: '${DOCKER}' not found on PATH" >&2
  exit 127
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ ! -d "${repo_root}/${POLICY_DIR}" ]]; then
  echo "scripts/cerbos.sh: no such policy directory: ${POLICY_DIR}" >&2
  exit 1
fi

exec "${DOCKER}" run --rm \
  --entrypoint /cerbos \
  -v "${repo_root}/${POLICY_DIR}:/policies:ro,z" \
  "${CERBOS_IMAGE}" "$@"
