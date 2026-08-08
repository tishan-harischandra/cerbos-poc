#!/usr/bin/env bash
# Runs kustomize in a container so no host install is needed, the same
# pattern as scripts/go.sh and scripts/k6.sh.
#
#   scripts/kustomize.sh build deploy/k8s/overlays/dev
set -euo pipefail

DOCKER="${DOCKER:-docker}"
KUSTOMIZE_IMAGE="${KUSTOMIZE_IMAGE:-registry.k8s.io/kustomize/kustomize:v5.4.3}"

if ! command -v "${DOCKER}" >/dev/null 2>&1; then
  echo "scripts/kustomize.sh: '${DOCKER}' not found on PATH" >&2
  exit 127
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

exec "${DOCKER}" run --rm \
  -v "${repo_root}:/repo:ro,z" \
  -w /repo \
  "${KUSTOMIZE_IMAGE}" "$@"
