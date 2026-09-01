#!/usr/bin/env bash
# The hospital switcher, end to end (issue #84), against a real `make up`
# stack.
#
# docs/MEASURED_FINDINGS.md#a-different-organization-scope-always-forces-re-authentication-issue-84
# is the investigation this suite is the committed, repeatable form of: a
# hospital switch cannot be made truly silent against this Keycloak
# version's Organizations feature, so what this proves is the mechanism
# HospitalSwitcher (libs/web/auth) actually depends on - a
# `prompt=none` request is refused rather than granted for an organization
# scope change, and the caller's existing session survives that refusal -
# plus that a real re-authentication still lets the same request reach the
# hospital it named.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
# shellcheck source=scripts/tests/lib-token.sh
source scripts/tests/lib-token.sh

failures=0
pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }

command -v jq >/dev/null 2>&1 || { echo "hospital-switch-e2e: jq is required" >&2; exit 1; }
wait_for_keycloak || exit 1

REDIRECT_URI="http://127.0.0.1:4200/"

# Same host-normalisation org-selector-e2e.sh needs: the login form's cookie
# is set for whatever host Keycloak was configured with, which need not be
# the host lib-token.sh's KEYCLOAK_URL uses to reach it.
BASE_URL="$(curl -sS --max-time 10 "${KEYCLOAK_URL}/realms/tenant-a/.well-known/openid-configuration" \
  | jq -r '.issuer' | sed 's#/realms/tenant-a$##')"
if [[ -z "${BASE_URL}" || "${BASE_URL}" == "null" ]]; then
  echo "hospital-switch-e2e: could not determine Keycloak's own hostname from discovery" >&2
  exit 1
fi
KEYCLOAK_URL="${BASE_URL}"

# desecure_cookie_jar <jar>
# Keycloak marks its session cookie Secure even on this http deployment
# (sslRequired: none is about the endpoint, not the cookie attribute), so a
# plain HTTP curl round trip never re-sends it and every request past the
# first would look cookie-less. A real browser has this cookie sent for it
# because production terminates TLS in front of Keycloak; this rewrites the
# jar's own Secure column so curl reproduces that over http.
desecure_cookie_jar() {
  local jar="$1"
  awk 'BEGIN{FS=OFS="\t"} NF==7 {$4="FALSE"} {print}' "${jar}" > "${jar}.tmp"
  mv "${jar}.tmp" "${jar}"
}

# authorize <cookie-jar> <scope> [extra curl args...]
# GETs the authorization endpoint with the given scope, using whatever SSO
# session the cookie jar already carries. Leaves the response headers in
# $authorize_headers and the response body in $authorize_body.
authorize() {
  local jar="$1" scope="$2"
  shift 2
  authorize_headers="$(curl -sS --max-time 10 -D - -o /tmp/hospital-switch-response.html -c "${jar}" -b "${jar}" \
    --get "${KEYCLOAK_URL}/realms/tenant-a/protocol/openid-connect/auth" \
    --data-urlencode "client_id=patient-app" \
    --data-urlencode "response_type=code" \
    --data-urlencode "scope=${scope}" \
    --data-urlencode "redirect_uri=${REDIRECT_URI}" \
    "$@")"
  authorize_body="$(cat /tmp/hospital-switch-response.html)"
  desecure_cookie_jar "${jar}"
}

login_action() {
  grep -o 'action="[^"]*"' <<<"$1" | head -1 | sed -e 's/action="//' -e 's/"$//' -e 's/&amp;/\&/g'
}

code_from_headers() {
  grep -i '^location:' <<<"$1" \
    | grep -o 'code=[^&[:space:]]*' \
    | head -1 \
    | cut -d= -f2- \
    | tr -d '\r'
}

error_from_headers() {
  grep -i '^location:' <<<"$1" \
    | grep -o 'error=[^&[:space:]]*' \
    | head -1 \
    | cut -d= -f2- \
    | tr -d '\r'
}

token_from_code() {
  curl -sS --max-time 10 \
    -d "grant_type=authorization_code" \
    -d "client_id=patient-app" \
    -d "code=$1" \
    -d "redirect_uri=${REDIRECT_URI}" \
    "${KEYCLOAK_URL}/realms/tenant-a/protocol/openid-connect/token"
}

# login <cookie-jar> <scope>
# Runs a full authorization + credentials submission, echoing the access
# token, or an empty string on failure.
login() {
  local jar="$1" scope="$2"
  authorize "${jar}" "${scope}"
  local action; action="$(login_action "${authorize_body}")"
  local headers
  headers="$(curl -sS --max-time 10 -D - -o /dev/null -c "${jar}" -b "${jar}" \
    --data-urlencode "username=user-doctor-multi" --data-urlencode "password=demo-password" \
    "${action}")"
  desecure_cookie_jar "${jar}"
  local code; code="$(code_from_headers "${headers}")"
  [[ -z "${code}" ]] && return 1
  jq -r '.access_token // empty' <<<"$(token_from_code "${code}")"
}

jar="$(mktemp)"

echo "--- an initial login establishes the SSO session and an active hospital ---"

first_token="$(login "${jar}" "openid organization:north-hospital")"
organization_claim="$(claim_of "${first_token}" '.organization | tojson' 2>/dev/null)"
if [[ "${organization_claim}" == '["north-hospital"]' ]]; then
  pass "the initial login yields a token scoped to north-hospital"
else
  fail "the initial login yields a token scoped to north-hospital (was ${organization_claim})"
fi

echo
echo "--- the token carries every membership for the switcher to offer (issue #84) ---"

memberships_claim="$(claim_of "${first_token}" '.organization_memberships | sort | tojson' 2>/dev/null)"
if [[ "${memberships_claim}" == '["north-hospital","south-hospital"]' ]]; then
  pass "the token's organization_memberships names every hospital the user belongs to"
else
  fail "the token's organization_memberships names every hospital the user belongs to (was ${memberships_claim})"
fi

echo
echo "--- a silent (prompt=none) switch to a real membership is refused, not granted ---"
echo "    (MEASURED_FINDINGS.md: Organizations forces re-authentication for any"
echo "     organization scope change, so this is the behaviour HospitalSwitcher"
echo "     actually depends on, not the one the PRD originally assumed)"

authorize "${jar}" "openid organization:south-hospital" --data-urlencode "prompt=none"
silent_error="$(error_from_headers "${authorize_headers}")"
silent_code="$(code_from_headers "${authorize_headers}")"
if [[ -n "${silent_error}" && -z "${silent_code}" ]]; then
  pass "a silent switch reaches an error, never a code or a screen"
else
  fail "a silent switch reaches an error, never a code or a screen (error=${silent_error}, code=${silent_code})"
fi

echo
echo "--- the existing session's own token is unaffected by the refused silent switch ---"

still_north="$(claim_of "${first_token}" '.organization | tojson' 2>/dev/null)"
if [[ "${still_north}" == '["north-hospital"]' ]]; then
  pass "the caller's own already-issued token still names its original hospital"
else
  fail "the caller's own already-issued token still names its original hospital (was ${still_north})"
fi

echo
echo "--- a real (non-silent) re-authentication still reaches the hospital named in scope ---"

jar2="$(mktemp)"
second_token="$(login "${jar2}" "openid organization:south-hospital")"
organization_claim2="$(claim_of "${second_token}" '.organization | tojson' 2>/dev/null)"
if [[ "${organization_claim2}" == '["south-hospital"]' ]]; then
  pass "a real re-authentication yields a token for the hospital the switch named"
else
  fail "a real re-authentication yields a token for the hospital the switch named (was ${organization_claim2})"
fi

echo
echo "--- a switch to an organization the user is not a member of fails outright ---"

authorize "${jar2}" "openid organization:a-hospital-nobody-belongs-to" --data-urlencode "prompt=none"
tampered_error="$(error_from_headers "${authorize_headers}")"
tampered_code="$(code_from_headers "${authorize_headers}")"
if [[ -n "${tampered_error}" && -z "${tampered_code}" ]]; then
  pass "a switch to an organization the user does not belong to reaches an error, never a code"
else
  fail "a switch to an organization the user does not belong to reaches an error, never a code (error=${tampered_error}, code=${tampered_code})"
fi

still_south="$(claim_of "${second_token}" '.organization | tojson' 2>/dev/null)"
if [[ "${still_south}" == '["south-hospital"]' ]]; then
  pass "the existing session's own token is unaffected by the refused switch"
else
  fail "the existing session's own token is unaffected by the refused switch (was ${still_south})"
fi

rm -f "${jar}" "${jar2}" /tmp/hospital-switch-response.html

if (( failures > 0 )); then
  echo
  echo "${failures} hospital-switch failure(s)"
  exit 1
fi

echo
echo "hospital switch end to end passed"
