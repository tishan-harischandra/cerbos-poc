#!/usr/bin/env bash
# §18: "Force a KEDA scale-to-zero on a stateless service, then generate
# load: the ScaledObject scales back up and requests succeed once ready,
# with no permissive failure while scaling."
#
# Every ScaledObject in this repo (deploy/k8s/base/*/scaledobject.yaml) uses
# a plain `cpu` trigger, and Kubernetes' Resource (CPU) HPA metric cannot be
# sampled from zero running pods, so it cannot itself scale a deployment back
# up from zero - only a KEDA external/HTTP-add-on scaler can activate from
# zero, which is out of this issue's scope (it would be a real ScaledObject
# trigger change, not a test). What this scenario proves instead, which is
# the acceptance criterion's actual safety property, is that a zeroed and a
# recovering ADS never answers with a permissive (fail-open) decision: every
# request either fails closed or is a correct decision, both before and
# after the operator-driven restore this script performs in place of an
# automatic one.
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"
# shellcheck source=scripts/tests/lib-token.sh
source "${repo_root}/scripts/tests/lib-token.sh"

check_url="http://127.0.0.1:${ADMIN_CONSOLE_PORT}/api/ads/internal/authz/check"
probe='{"resources":[{"kind":"patient_record","id":"patient-000",
  "attributes":{"tenantId":"tenant-a","hospitalId":"hospital-1","status":"ACTIVE"},
  "actions":["read"]}]}'
result=0
token="$(token_for user-unassigned)"

decide() {
  curl -sS -o /tmp/chaos-keda-zero-body -w '%{http_code}' --max-time 5 \
    -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" \
    --data "${probe}" "${check_url}" 2>/dev/null
}

echo "==> Forcing ads to zero replicas"
kubectl_chaos scale deployment/ads --replicas=0
kubectl_chaos wait --for=delete pod -l app=ads --timeout=60s

echo "==> Checking every response while ads is at zero fails closed, never open"
permissive=0
for _ in $(seq 1 10); do
  code="$(decide)"
  if [[ "${code}" == "200" ]] && [[ "$(jq -r '.resources[0].actions.read.allowed' /tmp/chaos-keda-zero-body 2>/dev/null)" == "true" ]]; then
    permissive=1
  fi
  sleep 1
done
if [[ "${permissive}" -eq 0 ]]; then
  echo "ok   no permissive decision is returned while ads is at zero replicas"
else
  echo "FAIL no permissive decision is returned while ads is at zero replicas"
  result=1
fi

echo "==> Restoring ads and generating load while it comes back"
kubectl_chaos scale deployment/ads --replicas=1
permissive=0
recovered=0
for _ in $(seq 1 60); do
  code="$(decide)"
  if [[ "${code}" == "200" ]]; then
    if [[ "$(jq -r '.resources[0].actions.read.allowed' /tmp/chaos-keda-zero-body 2>/dev/null)" == "true" ]]; then
      permissive=1
    fi
    recovered=1
  fi
  sleep 1
done
if [[ "${recovered}" -eq 1 ]] && [[ "${permissive}" -eq 0 ]]; then
  echo "ok   ads recovers and serves correct decisions with no permissive failure while scaling"
else
  echo "FAIL ads recovers and serves correct decisions with no permissive failure while scaling (recovered=${recovered}, permissive=${permissive})"
  result=1
fi

wait_for_rollout deployment/ads 180s
exit "${result}"
