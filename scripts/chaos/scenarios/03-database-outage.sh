#!/usr/bin/env bash
# §18: "PostgreSQL outage - Warm cached decisions continue until configured
# safety threshold; cache misses fail closed."
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"
# shellcheck source=scripts/tests/lib-token.sh
source "${repo_root}/scripts/tests/lib-token.sh"

check_url="http://127.0.0.1:${ADMIN_CONSOLE_PORT}/api/ads/internal/authz/check"
result=0

warm_probe='{"resources":[{"kind":"patient_record","id":"patient-warm-000",
  "attributes":{"tenantId":"tenant-a","hospitalId":"hospital-1","status":"ACTIVE"},
  "actions":["read"]}]}'
# A resource ADS has never assembled a permissionContext for, so this
# request can only be answered by a fresh read - exactly the cache miss the
# acceptance criterion says must fail closed, not open.
cold_probe='{"resources":[{"kind":"patient_record","id":"patient-cold-'"$(date +%s%N)"'",
  "attributes":{"tenantId":"tenant-a","hospitalId":"hospital-1","status":"ACTIVE"},
  "actions":["read"]}]}'

token="$(token_for user-unassigned)"

# This scenario is about the cache itself, not load balancing across it;
# see lib.sh's pin_to_one_replica for why that means one ads replica.
pin_to_one_replica ads

echo "==> Warming the cache for patient-warm-000"
warm_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
  -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" \
  --data "${warm_probe}" "${check_url}")"
if [[ "${warm_code}" != "200" ]]; then
  echo "FAIL database outage: could not warm the cache before blocking the database (HTTP ${warm_code})"
  exit 1
fi

echo "==> Blocking the database (scaling statefulset/postgres to 0)"
kubectl_chaos scale statefulset/postgres --replicas=0
kubectl_chaos wait --for=delete pod -l app=postgres --timeout=60s

warm_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
  -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" \
  --data "${warm_probe}" "${check_url}")"
if [[ "${warm_code}" == "200" ]]; then
  echo "ok   a warm decision continues to be served while the database is blocked"
else
  echo "FAIL a warm decision continues to be served while the database is blocked (HTTP ${warm_code})"
  result=1
fi

cold_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
  -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" \
  --data "${cold_probe}" "${check_url}")"
if [[ "${cold_code}" == "200" ]]; then
  echo "FAIL a cache miss fails closed while the database is blocked (got HTTP 200, a permissive failure)"
  result=1
else
  echo "ok   a cache miss fails closed while the database is blocked (HTTP ${cold_code}, not a permissive failure)"
fi

echo "==> Restoring the database"
kubectl_chaos scale statefulset/postgres --replicas=1
wait_for_rollout statefulset/postgres 120s

recovered=0
for _ in $(seq 1 30); do
  code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
    -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" \
    --data "${warm_probe}" "${check_url}")"
  [[ "${code}" == "200" ]] && { recovered=1; break; }
  sleep 1
done
if [[ "${recovered}" -eq 1 ]]; then
  echo "ok   decisions resume once the database is restored"
else
  echo "FAIL decisions resume once the database is restored"
  result=1
fi

exit "${result}"
