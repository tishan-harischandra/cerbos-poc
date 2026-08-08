#!/usr/bin/env bash
# §18: "Drain a node running Cerbos or ADS pods (kubectl drain): the
# scheduler reschedules onto remaining nodes and traffic continues with no
# failed authorization requests."
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"

out_file="$(mktemp)"
trap 'rm -f "${out_file}"' EXIT

node="$(kubectl_chaos get pods -l app=cerbos -o jsonpath='{.items[0].spec.nodeName}')"
if [[ -z "${node}" ]]; then
  echo "FAIL draining a node running Cerbos or ADS pods causes no failed authorization requests (no cerbos pod found)"
  exit 1
fi

# scripts/chaos/kind-config.yaml runs a control-plane and two workers so a
# drained node always has somewhere else to reschedule to; a
# CHAOS_KIND_CONFIG override with only one node (see lib.sh) cannot honour
# that, so this scenario is skipped rather than failed for a topology it
# never asked for.
node_count="$(kubectl_chaos_sys get nodes --no-headers | wc -l)"
if [[ "${node_count}" -lt 2 ]]; then
  echo "SKIP draining a node running Cerbos or ADS pods causes no failed authorization requests (only ${node_count} node in this cluster)"
  exit 0
fi

decision_probe_loop 45 "${out_file}" &
probe_pid=$!

sleep 5
echo "==> Draining node ${node}"
kubectl_chaos_sys drain "${node}" --ignore-daemonsets --delete-emptydir-data --force --timeout=60s
wait_for_rollout deployment/cerbos 120s
wait_for_rollout deployment/ads 120s

wait "${probe_pid}"
result=0
assert_no_failures "${out_file}" "draining a node running Cerbos or ADS pods causes no failed authorization requests" || result=1

echo "==> Uncordoning ${node}"
kubectl_chaos_sys uncordon "${node}"
exit "${result}"
