#!/usr/bin/env bash
# Runs k6 in a container so no host install is needed, the same pattern as
# scripts/cerbos.sh and scripts/go.sh.
#
#   scripts/k6.sh <k6-args...>
#
# K6_NETWORK attaches the container to a compose network, needed for `run`
# against a live stack but not for `inspect`, which only parses the script.
# K6_RESULTS_DIR, if set, is mounted at /output for --summary-export and
# handleSummary's own file output.
set -euo pipefail

DOCKER="${DOCKER:-docker}"
K6_IMAGE="${K6_IMAGE:-docker.io/grafana/k6:0.54.0}"

if ! command -v "${DOCKER}" >/dev/null 2>&1; then
  echo "scripts/k6.sh: '${DOCKER}' not found on PATH" >&2
  exit 127
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

network_args=()
if [[ -n "${K6_NETWORK:-}" ]]; then
  network_args=(--network "${K6_NETWORK}")
fi

results_args=()
if [[ -n "${K6_RESULTS_DIR:-}" ]]; then
  mkdir -p "${K6_RESULTS_DIR}"
  results_args=(-v "$(cd "${K6_RESULTS_DIR}" && pwd):/output:z")
fi

env_args=()
for name in ${K6_ENV_PASS:-}; do
  if [[ -n "${!name:-}" ]]; then
    env_args+=(-e "${name}=${!name}")
  fi
done

exec "${DOCKER}" run --rm \
  "${network_args[@]}" \
  "${results_args[@]}" \
  "${env_args[@]}" \
  -v "${repo_root}/deploy/loadtest/k6:/scripts:ro,z" \
  "${K6_IMAGE}" "$@"
