#!/usr/bin/env bash
# Tears down the chaos kind cluster and its port-forwards.
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"

stop_port_forwards

if [[ -x "${KIND}" ]] && "${KIND}" get clusters 2>/dev/null | grep -qx "${CHAOS_CLUSTER_NAME}"; then
  "${KIND}" delete cluster --name "${CHAOS_CLUSTER_NAME}"
fi
rm -f "${CHAOS_KUBECONFIG}"
