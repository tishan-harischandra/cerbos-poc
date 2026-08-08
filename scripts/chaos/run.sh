#!/usr/bin/env bash
# The `make chaos` entry point (issue #26): brings up the dev-chaos kind
# cluster, runs every scripts/chaos/scenarios/*.sh in order, reports a
# per-scenario pass/fail summary, and tears the cluster down. Every §18
# failure-table row has exactly one scenario file; ls'ing the directory is
# how this loop stays honest about covering every row as scenarios are added.
#
# CHAOS_KEEP_CLUSTER=1 skips the teardown, useful when iterating on a
# scenario locally.
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"

cleanup() {
  stop_port_forwards
  if [[ "${CHAOS_KEEP_CLUSTER:-0}" != "1" ]]; then
    bash "${repo_root}/scripts/chaos/cluster-down.sh"
  fi
}
trap cleanup EXIT

echo "==> Bringing up the chaos cluster"
if ! bash "${repo_root}/scripts/chaos/cluster-up.sh"; then
  echo "scripts/chaos/run.sh: cluster-up.sh failed; not running scenarios against a broken cluster" >&2
  exit 1
fi
# cluster-up.sh starts the port-forwards every scenario below reuses and
# deliberately leaves them running past its own exit (see its final `trap -
# EXIT`); this script's cleanup trap above is what eventually stops them.

passed=0
failed=0
declare -a failed_scenarios=()

for scenario in "${repo_root}"/scripts/chaos/scenarios/*.sh; do
  name="$(basename "${scenario}" .sh)"
  echo
  echo "==> Running ${name}"
  if bash "${scenario}"; then
    echo "PASS ${name}"
    passed=$((passed + 1))
  else
    echo "FAIL ${name}"
    failed=$((failed + 1))
    failed_scenarios+=("${name}")
  fi
done

echo
echo "${passed} scenarios passed, ${failed} failed"
if [[ "${failed}" -gt 0 ]]; then
  printf 'failed: %s\n' "${failed_scenarios[@]}"
fi
[[ "${failed}" -eq 0 ]]
