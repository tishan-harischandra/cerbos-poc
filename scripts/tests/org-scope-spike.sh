#!/usr/bin/env bash
# Spike (issue #75): can a non-browser client obtain an organization-scoped
# token from Keycloak?
#
# This is the one investigation the rest of the realm/tenant/organization
# rework (#74) depends on. The 1000-VU protocol-level load model (#41 in the
# PRD's user stories) assumes a direct grant with `scope=organization:<alias>`
# populates the same `organization` claim a browser login would get from the
# in-flow selection screen. If Keycloak only populates that claim for browser
# flows, the load model has to be redesigned before slice #87 is attempted.
# This suite is the evidence either way; see
# docs/MEASURED_FINDINGS.md#organization-scoped-tokens-without-a-browser-issue-75
# for the written finding the acceptance criteria requires.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
# shellcheck source=scripts/tests/lib-token.sh
source scripts/tests/lib-token.sh

failures=0
pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }

command -v jq >/dev/null 2>&1 || { echo "org-scope-spike: jq is required" >&2; exit 1; }
wait_for_keycloak || exit 1

# token_with_scope <username> <scope>
# Echoes the raw token response (not just the access token), so callers can
# inspect the granted `scope` alongside the claims.
token_with_scope() {
  local username="$1" scope="$2"
  curl -sS --max-time 10 \
    -d "grant_type=password" \
    -d "client_id=patient-app" \
    -d "username=${username}" \
    -d "password=${DEMO_PASSWORD}" \
    -d "scope=${scope}" \
    "${KEYCLOAK_URL}/realms/${REALM}/protocol/openid-connect/token"
}

echo "--- the realm fixture ---"

# The realm fixture (deploy/keycloak/realm-tenant-a.json) declares
# organizationsEnabled, two organizations - north-hospital and
# south-hospital - and user-doctor as a member of north-hospital (issue #78
# adds the rest of the demo principals north-hospital needs to take a
# decision, but user-doctor is the one this spike asserts on), via
# Keycloak's own realm import. Declarative membership import works with a
# "username" reference in the member object; an "id" reference silently fails
# import with a null-pointer deep in Keycloak's organization importer (tried
# and rejected while building this spike). That is itself a finding worth
# keeping: seed and onboarding code must add members through the Admin REST
# API, not by hand-writing "members" blocks with user ids.
admin_org_members() {
  local alias="$1" admin_token org_id
  admin_token="$(curl -sS --max-time 10 \
    -d "grant_type=password" -d "client_id=admin-cli" \
    -d "username=${KEYCLOAK_ADMIN:-admin}" -d "password=${KEYCLOAK_ADMIN_PASSWORD:-change-me}" \
    "${KEYCLOAK_URL}/realms/master/protocol/openid-connect/token" | jq -r '.access_token')"
  org_id="$(curl -sS --max-time 10 -H "Authorization: Bearer ${admin_token}" \
    "${KEYCLOAK_URL}/admin/realms/${REALM}/organizations?search=${alias}" | jq -r '.[0].id')"
  curl -sS --max-time 10 -H "Authorization: Bearer ${admin_token}" \
    "${KEYCLOAK_URL}/admin/realms/${REALM}/organizations/${org_id}/members" | jq -r '.[].username'
}

north_members="$(admin_org_members north-hospital)"
if grep -qx "user-doctor" <<<"${north_members}"; then
  pass "north-hospital has the member the fixture declares"
else
  fail "north-hospital has the member the fixture declares (was '${north_members}')"
fi

south_members="$(admin_org_members south-hospital)"
if [[ -n "${south_members}" ]] && ! grep -qx "user-doctor" <<<"${south_members}"; then
  pass "south-hospital's members do not include user-doctor, so it remains a clean negative fixture for that user"
else
  fail "south-hospital's members do not include user-doctor (was '${south_members}')"
fi

echo
echo "--- a direct grant, no browser involved ---"

response="$(token_with_scope user-doctor "openid organization:north-hospital")"
token="$(jq -r '.access_token // empty' <<<"${response}")"
if [[ -z "${token}" ]]; then
  fail "a direct grant with an organization scope returns a token (got: ${response})"
else
  pass "a direct grant with an organization scope returns a token"

  granted_scope="$(jq -r '.scope' <<<"${response}")"
  if [[ "${granted_scope}" == *"organization:north-hospital"* ]]; then
    pass "the granted scope names the organization"
  else
    fail "the granted scope names the organization (was '${granted_scope}')"
  fi

  # The claim's shape is the load model's contract: a JSON array of aliases,
  # not a map and not a single scalar. Record the shape exactly, not just
  # "truthy", so later slices parse it rather than guess (AC: exact shape).
  organization_claim="$(claim_of "${token}" '.organization | tojson')"
  if [[ "${organization_claim}" == '["north-hospital"]' ]]; then
    pass "the organization claim is a JSON array containing exactly the requested alias"
  else
    fail "the organization claim is a JSON array containing exactly the requested alias (was '${organization_claim}')"
  fi
fi

echo
echo "--- an alias the user does not belong to ---"

response="$(token_with_scope user-doctor "openid organization:south-hospital")"
token="$(jq -r '.access_token // empty' <<<"${response}")"
if [[ -z "${token}" ]]; then
  fail "Keycloak still issues a token when the requested organization is not a membership (got: ${response})"
else
  pass "Keycloak still issues a token when the requested organization is not a membership"

  # This is the load-bearing negative: Keycloak does not refuse the grant, it
  # silently drops the scope. A test (and any future caller) that only checks
  # the HTTP status would be fooled into thinking the request succeeded as
  # asked.
  granted_scope="$(jq -r '.scope' <<<"${response}")"
  if [[ "${granted_scope}" != *"organization:south-hospital"* ]]; then
    pass "the ungranted organization scope is silently dropped, not honoured"
  else
    fail "the ungranted organization scope is silently dropped, not honoured (scope was '${granted_scope}')"
  fi

  organization_claim="$(claim_of "${token}" '.organization // "absent"')"
  if [[ "${organization_claim}" == "absent" ]]; then
    pass "the resulting token carries no organization claim at all"
  else
    fail "the resulting token carries no organization claim at all (was '${organization_claim}')"
  fi
fi

echo
echo "--- an administrator with no organization membership at all ---"

response="$(token_with_scope user-admin "openid organization:north-hospital")"
token="$(jq -r '.access_token // empty' <<<"${response}")"
if [[ -n "${token}" ]]; then
  organization_claim="$(claim_of "${token}" '.organization // "absent"')"
  if [[ "${organization_claim}" == "absent" ]]; then
    pass "an administrator cannot acquire an organization scope by simply asking for it"
  else
    fail "an administrator cannot acquire an organization scope by simply asking for it (was '${organization_claim}')"
  fi
else
  fail "the admin token request itself failed (got: ${response})"
fi

if (( failures > 0 )); then
  echo
  echo "${failures} organization-scope-spike failure(s)"
  exit 1
fi

echo
echo "organization-scope spike passed: direct grant populates the organization claim"
