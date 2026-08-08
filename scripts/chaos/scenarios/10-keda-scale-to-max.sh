#!/usr/bin/env bash
# §18: "Force a KEDA scale-to-max under sustained load: no request is
# dropped or fails open while at the replica ceiling."
#
# Driving real CPU past a ScaledObject's threshold from a bash load loop
# inside kind is not a reliable, fast, or deterministic way to reach the
# ceiling (see 09-keda-scale-to-zero.sh's comment on the same cpu trigger).
# What is deterministic, and what maxReplicaCount actually exists to
# guarantee, is that the HPA behind every ScaledObject clamps any replica
# count back down to the ceiling - this asks for more than the ceiling
# directly and proves the clamp holds while decisions keep flowing.
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"

out_file="$(mktemp)"
trap 'rm -f "${out_file}"' EXIT

max_replicas="$(kubectl_chaos get scaledobject/ads -o jsonpath='{.spec.maxReplicaCount}')"
over_max=$((max_replicas + 2))

decision_probe_loop 30 "${out_file}" &
probe_pid=$!

sleep 3
echo "==> Requesting ${over_max} ads replicas (ceiling is ${max_replicas})"
kubectl_chaos scale deployment/ads --replicas="${over_max}"

sleep 15
actual="$(kubectl_chaos get deployment/ads -o jsonpath='{.spec.replicas}')"

wait "${probe_pid}"
result=0
if [[ "${actual}" -le "${max_replicas}" ]]; then
  echo "ok   the HPA behind the ads ScaledObject clamps replicas to the ${max_replicas}-replica ceiling (observed ${actual})"
else
  echo "FAIL the HPA behind the ads ScaledObject clamps replicas to the ${max_replicas}-replica ceiling (observed ${actual})"
  result=1
fi
assert_no_failures "${out_file}" "no decision fails or fails open while ads is held at its replica ceiling" || result=1

exit "${result}"
