#!/usr/bin/env bash
# §18/§19: "Stop the IdP admin API: console search degrades visibly, runtime
# token validation continues." Keycloak serves both the admin API and the
# realm's token/JWKS endpoints from the one deployment, so this scales
# Keycloak itself to zero: the acceptance criterion under test is that
# runtime authorization is unaffected, which it must be either way (the
# decision path never calls the IdP's admin API - only the ADS's already
# resolved permissionContext and the cached JWKS backing the tokens minted
# before the outage).
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

# Token signature validation is served from ads's own in-process JWKS
# cache, which - like the role-matrix cache lib.sh's pin_to_one_replica
# also exists for - is per replica, not shared; a decision that lands on a
# replica that never fetched the signing key while Keycloak was up 401s
# once Keycloak goes down, which is a replica-selection artifact of this
# test, not the fail-closed/fail-open property under test here.
pin_to_one_replica ads

echo "==> Minting a token and warming the JWKS cache before the outage"
token="$(token_for user-unassigned)"
# pin_to_one_replica's scale-down just above can leave the console's
# upstream connection to ads pointed at the pod that no longer exists
# until the cluster's networking converges on the survivor - a transient
# blackhole, not a signal about this scenario's own assertion - so this
# warm-up call is retried rather than trusted on its first attempt.
admin_token="$(token_for user-admin)"
warm_code=""
for _ in $(seq 1 10); do
  warm_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
    -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" \
    --data "${probe}" "${check_url}")"
  [[ "${warm_code}" == "200" ]] && break
  sleep 2
done
# admin-service verifies tokens against its own, separately cached JWKS
# (see the console diagnostics check below), so it needs its own warm
# request too - a decision through ads warms only ads's.
curl -sS -o /dev/null --max-time 5 -H "Authorization: Bearer ${admin_token}" \
  "http://127.0.0.1:${ADMIN_CONSOLE_PORT}/api/admin/idp/diagnostics"

echo "==> Stopping the IdP (scaling deployment/keycloak to 0)"
kubectl_chaos scale deployment/keycloak --replicas=0
kubectl_chaos wait --for=delete pod -l app=keycloak --timeout=60s

code=""
for _ in $(seq 1 10); do
  code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
    -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" \
    --data "${probe}" "${check_url}")"
  # Only a transport-level failure (curl never got a response at all) is
  # retried here - the same transient blackhole the warm-up above accounts
  # for. Any real HTTP response, including a non-200 one, is this
  # assertion's actual answer and must not be retried away.
  [[ "${code}" != "000" ]] && break
  sleep 2
done
if [[ "${code}" == "200" ]]; then
  echo "ok   runtime authorization continues while the IdP admin API is unavailable"
else
  echo "FAIL runtime authorization continues while the IdP admin API is unavailable (HTTP ${code})"
  result=1
fi

diag_url="http://127.0.0.1:${ADMIN_CONSOLE_PORT}/api/admin/idp/diagnostics"
diag_body="$(curl -sS --max-time 5 -H "Authorization: Bearer ${admin_token}" "${diag_url}")"
if [[ "$(jq -r '.connectivity' <<<"${diag_body}")" == "degraded" ]]; then
  echo "ok   console diagnostics visibly report the IdP as degraded"
else
  echo "FAIL console diagnostics visibly report the IdP as degraded (got: ${diag_body})"
  result=1
fi

echo "==> Restoring the IdP"
kubectl_chaos scale deployment/keycloak --replicas=1
wait_for_rollout deployment/keycloak 180s
# The scale-to-zero above deleted the pod the long-lived Keycloak
# port-forward from lib.sh's start_port_forwards was bound to; without
# this, every later scenario's token_for call fails against a tunnel that
# forwards to a pod that no longer exists (see restart_port_forward).
restart_port_forward svc/keycloak "${KEYCLOAK_PORT}" 8080

exit "${result}"
