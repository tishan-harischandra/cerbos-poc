#!/usr/bin/env bash
# §18: "ADS pod failure - Service routes to another replica; caches warm on
# demand."
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"

out_file="$(mktemp)"
trap 'rm -f "${out_file}"' EXIT

decision_probe_loop 20 "${out_file}" &
probe_pid=$!

sleep 5
victim="$(kubectl_chaos get pods -l app=ads -o jsonpath='{.items[0].metadata.name}')"
echo "==> Deleting ads pod ${victim}"
kubectl_chaos delete pod "${victim}" --wait=false

wait "${probe_pid}"
assert_no_failures "${out_file}" "killing an ADS replica causes no failed authorization requests"
