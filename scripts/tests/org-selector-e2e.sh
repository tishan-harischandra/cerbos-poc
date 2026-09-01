#!/usr/bin/env bash
# The organization selector authenticator (issue #79), end to end through
# the real browser login flow: an authorization code request, a cookie jar
# carrying the login session, a credentials POST, and the code exchanged for
# a token - no browser automation toolchain, just curl and a cookie jar in
# the style of the existing identity suites.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
# shellcheck source=scripts/tests/lib-token.sh
source scripts/tests/lib-token.sh

failures=0
pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }

command -v jq >/dev/null 2>&1 || { echo "org-selector-e2e: jq is required" >&2; exit 1; }
wait_for_keycloak || exit 1

REDIRECT_URI="http://127.0.0.1:4200/"

# login_page <realm> <client-id> <cookie-jar> [scope]
# GETs the authorization endpoint and leaves the login form in $login_body.
login_page() {
  local realm="$1" client="$2" jar="$3" scope="${4:-openid}"
  login_body="$(curl -sS --max-time 10 -c "${jar}" \
    --get "${KEYCLOAK_URL}/realms/${realm}/protocol/openid-connect/auth" \
    --data-urlencode "client_id=${client}" \
    --data-urlencode "response_type=code" \
    --data-urlencode "scope=${scope}" \
    --data-urlencode "redirect_uri=${REDIRECT_URI}")"
}

# login_action <login-html>
# Echoes the login form's own action URL, unescaped.
login_action() {
  grep -o 'action="[^"]*"' <<<"$1" | head -1 | sed -e 's/action="//' -e 's/"$//' -e 's/&amp;/\&/g'
}

# submit_credentials <action-url> <cookie-jar> <username> <password>
# Submits the login form, leaving the response headers in $login_headers.
submit_credentials() {
  local action="$1" jar="$2" username="$3" password="$4"
  login_headers="$(curl -sS --max-time 10 -D - -o /dev/null -c "${jar}" -b "${jar}" \
    --data-urlencode "username=${username}" \
    --data-urlencode "password=${password}" \
    "${action}")"
}

# code_from_redirect
# Reads the authorization code out of $login_headers's Location header, or
# echoes nothing if the flow did not redirect (a refusal, or an error page).
code_from_redirect() {
  grep -i '^location:' <<<"${login_headers}" \
    | grep -o 'code=[^&[:space:]]*' \
    | head -1 \
    | cut -d= -f2- \
    | tr -d '\r'
}

# token_from_code <realm> <client-id> <code>
# Exchanges an authorization code for a token response.
token_from_code() {
  local realm="$1" client="$2" code="$3"
  curl -sS --max-time 10 \
    -d "grant_type=authorization_code" \
    -d "client_id=${client}" \
    -d "code=${code}" \
    -d "redirect_uri=${REDIRECT_URI}" \
    "${KEYCLOAK_URL}/realms/${realm}/protocol/openid-connect/token"
}

echo "--- an alias already requested in scope is selected with no screen ---"

jar="$(mktemp)"
login_page tenant-a patient-app "${jar}" "openid organization:north-hospital"
action="$(login_action "${login_body}")"
submit_credentials "${action}" "${jar}" user-doctor demo-password
code="$(code_from_redirect)"
if [[ -z "${code}" ]]; then
  fail "an alias already in scope reaches a code (headers were: ${login_headers})"
else
  response="$(token_from_code tenant-a patient-app "${code}")"
  granted_scope="$(jq -r '.scope // empty' <<<"${response}")"
  if [[ "${granted_scope}" == *"organization:north-hospital"* ]]; then
    pass "an alias already requested in scope is selected with no screen"
  else
    fail "an alias already requested in scope is selected with no screen (scope was '${granted_scope}')"
  fi
fi
rm -f "${jar}"

echo
echo "--- a single membership is auto-selected with no screen ---"

jar="$(mktemp)"
login_page tenant-a patient-app "${jar}"
action="$(login_action "${login_body}")"
submit_credentials "${action}" "${jar}" user-doctor demo-password
code="$(code_from_redirect)"
if [[ -z "${code}" ]]; then
  fail "a single membership reaches a code with no scope requested (headers were: ${login_headers})"
else
  response="$(token_from_code tenant-a patient-app "${code}")"
  access_token="$(jq -r '.access_token // empty' <<<"${response}")"
  organization_claim="$(claim_of "${access_token}" '.organization | tojson')"
  if [[ "${organization_claim}" == '["north-hospital"]' ]]; then
    pass "a single membership is auto-selected with no screen"
  else
    fail "a single membership is auto-selected with no screen (organization claim was '${organization_claim}')"
  fi
fi
rm -f "${jar}"

echo
echo "--- no membership and not an administrator is refused with an explicit reason ---"

jar="$(mktemp)"
login_page tenant-a patient-app "${jar}"
action="$(login_action "${login_body}")"
refusal="$(curl -sS --max-time 10 -o /tmp/org-selector-refusal.html -w '%{http_code}' \
  -c "${jar}" -b "${jar}" \
  --data-urlencode "username=user-forger" --data-urlencode "password=demo-password" \
  "${action}")"
if [[ "${refusal}" == "403" ]]; then
  pass "no membership and not an administrator is refused rather than shown a generic failure"
else
  fail "no membership and not an administrator is refused (HTTP ${refusal})"
fi
if grep -qF "your account is not attached to a hospital" /tmp/org-selector-refusal.html; then
  pass "the refusal names an explicit reason"
else
  fail "the refusal names an explicit reason (page did not contain it)"
fi
rm -f "${jar}" /tmp/org-selector-refusal.html

echo
echo "--- a user cannot obtain a token scoped to an organization they do not belong to ---"

jar="$(mktemp)"
login_page tenant-a patient-app "${jar}" "openid organization:south-hospital"
action="$(login_action "${login_body}")"
submit_credentials "${action}" "${jar}" user-doctor demo-password
code="$(code_from_redirect)"
if [[ -z "${code}" ]]; then
  fail "a membership-mismatched alias still reaches a decision (headers were: ${login_headers})"
else
  # user-doctor belongs to north-hospital only, not south-hospital: the
  # requested alias does not match, so the auto-select rule applies to the
  # membership Keycloak actually confirmed, not the one asked for.
  response="$(token_from_code tenant-a patient-app "${code}")"
  access_token="$(jq -r '.access_token // empty' <<<"${response}")"
  organization_claim="$(claim_of "${access_token}" '.organization | tojson')"
  if [[ "${organization_claim}" == '["north-hospital"]' ]]; then
    pass "a user cannot obtain a token scoped to an organization they do not belong to"
  else
    fail "a user cannot obtain a token scoped to an organization they do not belong to (organization claim was '${organization_claim}')"
  fi
fi
rm -f "${jar}"

echo
echo "--- an administrator is undecided (the selection screen, not this authenticator) ---"

jar="$(mktemp)"
login_page tenant-a patient-app "${jar}"
action="$(login_action "${login_body}")"
submit_credentials "${action}" "${jar}" user-admin demo-password
code="$(code_from_redirect)"
if [[ -z "${code}" ]]; then
  fail "an administrator still reaches a code (headers were: ${login_headers})"
else
  response="$(token_from_code tenant-a patient-app "${code}")"
  access_token="$(jq -r '.access_token // empty' <<<"${response}")"
  organization_claim="$(claim_of "${access_token}" '.organization // "absent"')"
  if [[ "${organization_claim}" == "absent" ]]; then
    pass "an administrator's login proceeds with no organization forced, unchanged by this authenticator"
  else
    fail "an administrator's login proceeds with no organization forced (organization claim was '${organization_claim}')"
  fi
fi
rm -f "${jar}"

if (( failures > 0 )); then
  echo
  echo "${failures} organization-selector failure(s)"
  exit 1
fi

echo
echo "organization selector end to end passed"
