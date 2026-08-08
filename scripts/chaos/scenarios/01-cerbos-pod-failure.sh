#!/usr/bin/env bash
# §18: "Cerbos pod failure - Kubernetes routes to another replica; no sticky
# session required."
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"

out_file="$(mktemp)"
trap 'rm -f "${out_file}"' EXIT

decision_probe_loop 20 "${out_file}" &
probe_pid=$!

sleep 5
victim="$(kubectl_chaos get pods -l app=cerbos -o jsonpath='{.items[0].metadata.name}')"
echo "==> Deleting cerbos pod ${victim}"
kubectl_chaos delete pod "${victim}" --wait=false

wait "${probe_pid}"
assert_no_failures "${out_file}" "killing a Cerbos replica causes no failed authorization requests"
