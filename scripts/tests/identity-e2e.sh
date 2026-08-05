#!/usr/bin/env bash
# Identity end to end against a running `make up` stack.
#
# The unit suites prove each check in isolation. This proves the whole identity
# path: an OIDC login against a real Keycloak, a real signature over a real key
# set, and an ADS that accepts exactly the tokens it should and refuses the
# rest. Token expiry is left to the unit suite - waiting out a real token's
# lifetime would make this suite slow for no extra evidence.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
# shellcheck source=scripts/tests/lib-token.sh
source scripts/tests/lib-token.sh

PORT="${ADMIN_CONSOLE_PORT:-4200}"
ADS_URL="http://127.0.0.1:${PORT}/api/ads"
failures=0

command -v jq >/dev/null 2>&1 || { echo "identity-e2e: jq is required" >&2; exit 1; }
wait_for_keycloak || exit 1

pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }

expect_status() {
  local description="$1" expected="$2" actual="$3" body="${4:-}"
  if [[ "${actual}" == "${expected}" ]]; then
    pass "${description}"
  else
    fail "${description} (HTTP ${actual}, want ${expected}${body:+: ${body}})"
  fi
}

READ_REQUEST='{"resources":[{"kind":"patient_record","id":"patient-456","attributes":{"tenantId":"tenant-a","hospitalId":"hospital-1","status":"ACTIVE"},"actions":["read"]}]}'

# decide_with <token> [body]
# Echoes the HTTP status of a decision call, leaving the body in /tmp/identity-body.
decide_with() {
  local token="$1" body="${2:-${READ_REQUEST}}"
  local auth=()
  [[ -n "${token}" ]] && auth=(-H "Authorization: Bearer ${token}")

  curl -sS -o /tmp/identity-body -w '%{http_code}' \
    -H 'Content-Type: application/json' \
    "${auth[@]}" \
    --data "${body}" \
    "${ADS_URL}/internal/authz/check"
}

echo "--- a real OIDC login ---"

doctor_token="$(token_for user-doctor)" || exit 1
pass "a user can log in against Keycloak and receive a token"

roles="$(claim_of "${doctor_token}" '.resource_access["patient-app"].roles | join(",")')"
if [[ "${roles}" == *"doctor"* ]]; then
  pass "the token carries the user's roles"
else
  fail "the token carries the user's roles (roles were '${roles}')"
fi

subject="$(claim_of "${doctor_token}" '.sub')"
if [[ "${subject}" == "user-doctor" ]]; then
  pass "the token's subject is the identifier the authorization database keys on"
else
  fail "the token's subject is the identifier the authorization database keys on (was '${subject}')"
fi

tenant="$(claim_of "${doctor_token}" '.tenant_id')"
if [[ "${tenant}" == "tenant-a" ]]; then
  pass "the token carries the tenant the ADS will derive its context from"
else
  fail "the token carries the tenant (was '${tenant}')"
fi

echo
echo "--- what the ADS accepts ---"

expect_status "a valid token is accepted" 200 "$(decide_with "${doctor_token}")" "$(cat /tmp/identity-body)"

echo
echo "--- what the ADS refuses ---"

expect_status "no token at all is refused" 401 "$(decide_with "")"
expect_status "a tampered signature is refused" 401 "$(decide_with "$(tamper_with "${doctor_token}")")"

# Signed by Keycloak, for a real user, and still not for this service: the
# audience check is the only thing standing between the two.
other_audience_token="$(token_for user-doctor reporting-app)" || exit 1
expect_status "a token minted for another client is refused" 401 \
  "$(decide_with "${other_audience_token}")"

# The master realm is a different issuer with a different key set.
master_token="$(token_for "${KEYCLOAK_ADMIN:-admin}" admin-cli master)" || master_token=""
if [[ -n "${master_token}" ]]; then
  expect_status "a token from another issuer is refused" 401 "$(decide_with "${master_token}")"
else
  fail "a token from another issuer is refused (could not obtain a master realm token)"
fi

# §16.1: the synthetic role prefix is the platform's. The realm carries a
# hostile fixture role so this is a token Keycloak really issued.
forger_token="$(token_for user-forger)" || exit 1
expect_status "a token carrying a sys: role is refused outright" 403 "$(decide_with "${forger_token}")"

echo
echo "--- the browser cannot name itself ---"

smuggled='{"tenantId":"tenant-b","resources":[{"kind":"patient_record","id":"patient-456","attributes":{"tenantId":"tenant-b","hospitalId":"hospital-1","status":"ACTIVE"},"actions":["read"]}]}'
expect_status "a request naming its own tenant is refused" 400 \
  "$(decide_with "${doctor_token}" "${smuggled}")"

echo
echo "--- the identity directory ---"

admin_token="$(token_for user-admin)" || exit 1
status="$(curl -sS -o /tmp/identity-body -w '%{http_code}' \
  -H "Authorization: Bearer ${admin_token}" \
  "${ADS_URL}/internal/directory/users?limit=2")"
expect_status "user search answers" 200 "${status}" "$(cat /tmp/identity-body)"

if [[ "${status}" == "200" ]]; then
  count="$(jq -r '.items | length' /tmp/identity-body)"
  has_more="$(jq -r '.hasMore' /tmp/identity-body)"
  if [[ "${count}" == "2" && "${has_more}" == "true" ]]; then
    pass "user search returns a page and reports that another follows"
  else
    fail "user search returns a page and reports that another follows (${count} items, hasMore=${has_more})"
  fi

  second="$(curl -sS "${ADS_URL}/internal/directory/users?limit=2&offset=2" \
    -H "Authorization: Bearer ${admin_token}" | jq -r '.items[0].externalId')"
  first="$(jq -r '.items[0].externalId' /tmp/identity-body)"
  if [[ -n "${second}" && "${second}" != "${first}" ]]; then
    pass "the next page is a different window over the directory"
  else
    fail "the next page is a different window over the directory (both started at '${first}')"
  fi
fi

status="$(curl -sS -o /tmp/identity-body -w '%{http_code}' \
  -H "Authorization: Bearer ${admin_token}" \
  "${ADS_URL}/internal/directory/roles?limit=10")"
expect_status "role search answers" 200 "${status}" "$(cat /tmp/identity-body)"

if [[ "${status}" == "200" ]]; then
  # §7.5: the identifier the directory reports has to be the one a token
  # normalises to, or the console would write matrix rows nothing matches.
  canonical="$(jq -r '.items[] | select(.name == "doctor") | .canonicalId' /tmp/identity-body)"
  if [[ "${canonical}" == "kc:cerbos-poc:patient-app:doctor" ]]; then
    pass "the directory's canonical identifier matches token normalisation byte for byte"
  else
    fail "the directory's canonical identifier matches token normalisation (was '${canonical}')"
  fi
fi

expect_status "a directory query naming its own tenant is refused" 400 \
  "$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer ${admin_token}" \
    "${ADS_URL}/internal/directory/users?tenantId=tenant-b")"
expect_status "the directory is not readable without a token" 401 \
  "$(curl -sS -o /dev/null -w '%{http_code}' "${ADS_URL}/internal/directory/users")"

echo
echo "--- the admin credential stays server side ---"

# Everything a browser can reach, checked for the one string that must never
# appear in any of it.
secret="$(tr -d '[:space:]' < deploy/secrets/idp-admin-credentials)"
leaked=0
for path in "/internal/directory/users?limit=5" "/internal/directory/roles?limit=5" "/readyz" "/healthz"; do
  body="$(curl -sS -H "Authorization: Bearer ${admin_token}" "${ADS_URL}${path}")"
  if grep -qF "${secret}" <<<"${body}"; then
    fail "the IdP admin credential appeared in ${path}"
    leaked=1
  fi
done
if (( leaked == 0 )); then
  pass "no browser-reachable ADS response carries the IdP admin credential"
fi

rm -f /tmp/identity-body

if (( failures > 0 )); then
  echo
  echo "${failures} identity failure(s)"
  exit 1
fi

echo
echo "identity end to end passed"
