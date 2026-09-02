#!/usr/bin/env bash
# Tenant onboarding through the Admin Service, end to end (issue #86):
# tenant-c exists in Keycloak but starts out unregistered - not in
# deploy/tenant-registry.yaml - so onboarding it here, then logging in and
# reaching a decision against the same running stack, proves onboarding is
# real rather than a claim about a realm that was already registered.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
# shellcheck source=scripts/tests/lib-token.sh
source scripts/tests/lib-token.sh

PORT="${ADMIN_CONSOLE_PORT:-4200}"
ADMIN_URL="http://127.0.0.1:${PORT}/api/admin"
ADS_URL="http://127.0.0.1:${PORT}/api/ads"
failures=0

command -v jq >/dev/null 2>&1 || { echo "tenant-onboarding-e2e: jq is required" >&2; exit 1; }
wait_for_keycloak || exit 1

pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }

# An authenticated caller from any already-registered realm may onboard a
# new one (this prototype's administration surface trusts an authenticated
# caller rather than a role, everywhere else too - see
# docs/MEASURED_FINDINGS.md).
operator_token="$(token_for user-admin)" || exit 1

onboard() {
  curl -sS --max-time 10 -o /tmp/tenant-onboarding-body -w '%{http_code}' \
    -X POST -H "Authorization: Bearer ${operator_token}" -H 'Content-Type: application/json' \
    --data "$1" \
    "${ADMIN_URL}/authz/tenants"
}

# The issuer a token actually carries is whatever KC_HOSTNAME names
# (docker-compose.yml's KEYCLOAK_HOSTNAME, "http://localhost:8081" by
# default), which need not be the same host this script reaches Keycloak
# by (KEYCLOAK_URL, "127.0.0.1" here) - the same distinction
# provider.Config.JWKSURL's own doc comment makes.
TOKEN_ISSUER_HOST="${KEYCLOAK_HOSTNAME:-http://localhost:8081}"

echo "--- onboarding tenant-c, which exists only in Keycloak so far ---"

status="$(onboard '{
  "realm": "tenant-c",
  "issuer": "'"${TOKEN_ISSUER_HOST}"'/realms/tenant-c",
  "browserClientId": "patient-app",
  "serviceClientId": "authorization-admin-service",
  "credentialSecretRef": "/run/secrets/idp-admin-credentials-tenant-c"
}')"
if [[ "${status}" == "201" ]]; then
  pass "the onboarding request is accepted"
else
  fail "the onboarding request is accepted (HTTP ${status}: $(cat /tmp/tenant-onboarding-body))"
fi

echo
echo "--- onboarding the same realm again is rejected, not silently repeated ---"

status="$(onboard '{
  "realm": "tenant-c",
  "issuer": "'"${KEYCLOAK_URL}"'/realms/tenant-c",
  "browserClientId": "patient-app",
  "credentialSecretRef": "/run/secrets/idp-admin-credentials-tenant-c"
}')"
if [[ "${status}" == "409" ]]; then
  pass "onboarding a duplicate realm is rejected with a clear error"
else
  fail "onboarding a duplicate realm is rejected (HTTP ${status}: $(cat /tmp/tenant-onboarding-body))"
fi

echo
echo "--- onboarding an invalid entry is rejected before anything is saved ---"

status="$(onboard '{"realm": "tenant-d"}')"
if [[ "${status}" == "400" ]]; then
  pass "an entry missing required fields is rejected"
else
  fail "an entry missing required fields is rejected (HTTP ${status}: $(cat /tmp/tenant-onboarding-body))"
fi
rm -f /tmp/tenant-onboarding-body

echo
echo "--- the onboarded tenant becomes usable with no service restart ---"

# The login itself only ever depended on Keycloak, which has known about
# tenant-c since the stack came up; it is the decision below that depends
# on tenantdiscovery's own poll (DefaultInterval, 5s) having run.
doctor_token="$(token_for user-doctor-c patient-app tenant-c 2>/dev/null)"
if [[ -n "${doctor_token}" ]]; then
  pass "a login against the newly onboarded realm succeeds"
else
  fail "a login against the newly onboarded realm succeeds"
fi

if [[ -n "${doctor_token}" ]]; then
  request='{"resources":[{"kind":"patient_record","id":"patient-1","attributes":{"tenantId":"tenant-c","hospitalId":"hospital-c1","status":"ACTIVE"},"actions":["read"]}]}'
  decision_status=""
  for _ in $(seq 1 20); do
    decision_status="$(curl -sS --max-time 10 -o /tmp/tenant-onboarding-decision -w '%{http_code}' \
      -H "Authorization: Bearer ${doctor_token}" -H 'Content-Type: application/json' \
      --data "${request}" \
      "${ADS_URL}/internal/authz/check")"
    [[ "${decision_status}" == "200" ]] && break
    sleep 1
  done
  if [[ "${decision_status}" == "200" ]]; then
    pass "the ADS reaches a decision for the newly onboarded tenant, with no restart"
  else
    fail "the ADS reaches a decision for the newly onboarded tenant (HTTP ${decision_status}: $(cat /tmp/tenant-onboarding-decision 2>/dev/null))"
  fi
  rm -f /tmp/tenant-onboarding-decision
else
  fail "the ADS reaches a decision for the newly onboarded tenant (skipped: no token)"
fi

if (( failures > 0 )); then
  echo
  echo "${failures} tenant-onboarding failure(s)"
  exit 1
fi

echo
echo "tenant onboarding end to end passed"
