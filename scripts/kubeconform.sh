#!/usr/bin/env bash
# Runs kubeconform in a container, reading manifests on stdin, the same
# pattern as scripts/kustomize.sh.
#
#   scripts/kustomize.sh build deploy/k8s/overlays/dev | scripts/kubeconform.sh
set -euo pipefail

DOCKER="${DOCKER:-docker}"
KUBECONFORM_IMAGE="${KUBECONFORM_IMAGE:-ghcr.io/yannh/kubeconform:latest-alpine}"

if ! command -v "${DOCKER}" >/dev/null 2>&1; then
  echo "scripts/kubeconform.sh: '${DOCKER}' not found on PATH" >&2
  exit 127
fi

exec "${DOCKER}" run --rm -i "${KUBECONFORM_IMAGE}" "$@"
