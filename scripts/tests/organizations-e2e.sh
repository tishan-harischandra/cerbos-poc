#!/usr/bin/env bash
# The identity directory's organization reads, end to end (issue #85):
# a real Keycloak, a real service-account token, and the ADS's own proxy
# through to the Admin Console.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
# shellcheck source=scripts/tests/lib-token.sh
source scripts/tests/lib-token.sh

PORT="${ADMIN_CONSOLE_PORT:-4200}"
ADS_URL="http://127.0.0.1:${PORT}/api/ads"
failures=0

command -v jq >/dev/null 2>&1 || { echo "organizations-e2e: jq is required" >&2; exit 1; }
wait_for_keycloak || exit 1

pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }

admin_token="$(token_for user-admin)" || exit 1

echo "--- organizations of a tenant ---"

response="$(curl -sS --max-time 10 -H "Authorization: Bearer ${admin_token}" \
  "${ADS_URL}/internal/directory/organizations")"
aliases="$(jq -r '.items[].alias' <<<"${response}" 2>/dev/null | sort)"
if [[ "${aliases}" == $'north-hospital\nsouth-hospital' ]]; then
  pass "the tenant's own organizations are listed"
else
  fail "the tenant's own organizations are listed (was: ${response})"
fi

north_id="$(jq -r '.items[] | select(.alias == "north-hospital") | .externalId' <<<"${response}")"
if [[ -z "${north_id}" || "${north_id}" == "null" ]]; then
  fail "north-hospital's external id could not be read (headers were: ${response})"
fi

echo
echo "--- members of an organization ---"

if [[ -n "${north_id}" && "${north_id}" != "null" ]]; then
  members_response="$(curl -sS --max-time 10 -H "Authorization: Bearer ${admin_token}" \
    "${ADS_URL}/internal/directory/organizations/${north_id}/members")"
  usernames="$(jq -r '.items[].username' <<<"${members_response}" 2>/dev/null | sort)"
  if grep -qx "user-doctor" <<<"${usernames}"; then
    pass "the organization's own members are listed"
  else
    fail "the organization's own members are listed (was: ${members_response})"
  fi
fi

echo
echo "--- organizations of a user ---"

user_orgs_response="$(curl -sS --max-time 10 -H "Authorization: Bearer ${admin_token}" \
  "${ADS_URL}/internal/directory/users/user-doctor-multi/organizations")"
user_aliases="$(jq -r '.items[].alias' <<<"${user_orgs_response}" 2>/dev/null | sort)"
if [[ "${user_aliases}" == $'north-hospital\nsouth-hospital' ]]; then
  pass "a user's own memberships are listed"
else
  fail "a user's own memberships are listed (was: ${user_orgs_response})"
fi

echo
echo "--- a cross-tenant lookup is refused, not filtered or empty ---"

status="$(curl -sS --max-time 10 -o /tmp/organizations-cross-tenant.json -w '%{http_code}' \
  -H "Authorization: Bearer ${admin_token}" \
  --get "${ADS_URL}/internal/directory/organizations" --data-urlencode "tenantId=tenant-b")"
if [[ "${status}" == "400" ]]; then
  pass "naming another tenant in the request is refused outright"
else
  fail "naming another tenant in the request is refused outright (HTTP ${status}: $(cat /tmp/organizations-cross-tenant.json))"
fi
rm -f /tmp/organizations-cross-tenant.json

echo
echo "--- the admin credential stays server side ---"

if curl -sS --max-time 10 -H "Authorization: Bearer ${admin_token}" \
  "${ADS_URL}/internal/directory/organizations" | grep -qi "client_secret\|password"; then
  fail "the organizations response echoed a credential"
else
  pass "no browser-reachable response carries the IdP admin credential"
fi

if (( failures > 0 )); then
  echo
  echo "${failures} organizations failure(s)"
  exit 1
fi

echo
echo "organizations end to end passed"
