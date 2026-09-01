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

# The browser flow's login form action and its Set-Cookie both carry
# whatever host Keycloak was configured with (KC_HOSTNAME), which need not
# be the host lib-token.sh's KEYCLOAK_URL uses to reach it (compose
# defaults it to "localhost"; KEYCLOAK_URL defaults to "127.0.0.1" -
# harmless for every other e2e script here, since a direct grant carries no
# cookie, but fatal for a cookie jar: a cookie set for one host is not sent
# on a request to the other, and the login form fails with "Restart login
# cookie not found" rather than anything that names the real problem).
# Discovery's own issuer is read once, up front, so every request in this
# script goes to the one host Keycloak actually issues cookies for.
BASE_URL="$(curl -sS --max-time 10 "${KEYCLOAK_URL}/realms/tenant-a/.well-known/openid-configuration" \
  | jq -r '.issuer' | sed 's#/realms/tenant-a$##')"
if [[ -z "${BASE_URL}" || "${BASE_URL}" == "null" ]]; then
  echo "org-selector-e2e: could not determine Keycloak's own hostname from discovery" >&2
  exit 1
fi
KEYCLOAK_URL="${BASE_URL}"

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

# submit_organization <action-url> <cookie-jar> <alias>
# Submits the selection screen's own form, leaving the response headers in
# $login_headers.
submit_organization() {
  local action="$1" jar="$2" alias="$3"
  login_headers="$(curl -sS --max-time 10 -D - -o /tmp/org-selector-submission.html -c "${jar}" -b "${jar}" \
    --data-urlencode "organization=${alias}" \
    "${action}")"
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
echo "--- an administrator sees a tenant-wide entry; a non-administrator never does (issue #81) ---"

jar="$(mktemp)"
login_page tenant-a patient-app "${jar}"
action="$(login_action "${login_body}")"
form_body="$(curl -sS --max-time 10 -c "${jar}" -b "${jar}" \
  --data-urlencode "username=user-admin" --data-urlencode "password=demo-password" \
  "${action}")"
if grep -qF "Tenant-wide" <<<"${form_body}"; then
  pass "a user holding the admin realm role sees a tenant-wide entry"
else
  fail "a user holding the admin realm role sees a tenant-wide entry (screen was: ${form_body})"
fi
selection_action="$(login_action "${form_body}")"

jar2="$(mktemp)"
login_page tenant-a patient-app "${jar2}"
action2="$(login_action "${login_body}")"
doctor_form="$(curl -sS --max-time 10 -c "${jar2}" -b "${jar2}" \
  --data-urlencode "username=user-doctor-multi" --data-urlencode "password=demo-password" \
  "${action2}")"
if grep -qF "Tenant-wide" <<<"${doctor_form}"; then
  fail "a user without the admin realm role never sees the tenant-wide entry (screen was: ${doctor_form})"
else
  pass "a user without the admin realm role never sees the tenant-wide entry"
fi
rm -f "${jar2}"

echo
echo "--- an administrator with no organization memberships can still log in, choosing tenant-wide ---"

submit_organization "${selection_action}" "${jar}" ""
code="$(code_from_redirect)"
if [[ -z "${code}" ]]; then
  fail "an administrator choosing tenant-wide reaches a code (headers were: ${login_headers})"
else
  response="$(token_from_code tenant-a patient-app "${code}")"
  access_token="$(jq -r '.access_token // empty' <<<"${response}")"
  organization_claim="$(claim_of "${access_token}" '.organization // "absent"')"
  if [[ "${organization_claim}" == "absent" ]]; then
    pass "a tenant-wide session carries no active hospital"
  else
    fail "a tenant-wide session carries no active hospital (organization claim was '${organization_claim}')"
  fi
fi
rm -f "${jar}"

echo
echo "--- an administrator who also belongs to organizations can still choose one of them ---"

jar="$(mktemp)"
login_page tenant-a patient-app "${jar}"
action="$(login_action "${login_body}")"
form_body="$(curl -sS --max-time 10 -c "${jar}" -b "${jar}" \
  --data-urlencode "username=user-admin-clinician" --data-urlencode "password=demo-password" \
  "${action}")"
selection_action="$(login_action "${form_body}")"
submit_organization "${selection_action}" "${jar}" "north-hospital"
code="$(code_from_redirect)"
if [[ -z "${code}" ]]; then
  fail "an administrator choosing a hospital reaches a code (headers were: ${login_headers})"
else
  response="$(token_from_code tenant-a patient-app "${code}")"
  access_token="$(jq -r '.access_token // empty' <<<"${response}")"
  organization_claim="$(claim_of "${access_token}" '.organization | tojson')"
  if [[ "${organization_claim}" == '["north-hospital"]' ]]; then
    pass "an administrator who also belongs to organizations can choose one and receive an ordinary hospital-scoped session"
  else
    fail "an administrator can choose a hospital instead of tenant-wide (organization claim was '${organization_claim}')"
  fi
fi
rm -f "${jar}"

echo
echo "--- a non-administrator cannot forge a tenant-wide submission ---"

jar="$(mktemp)"
login_page tenant-a patient-app "${jar}"
action="$(login_action "${login_body}")"
form_body="$(curl -sS --max-time 10 -c "${jar}" -b "${jar}" \
  --data-urlencode "username=user-doctor-multi" --data-urlencode "password=demo-password" \
  "${action}")"
selection_action="$(login_action "${form_body}")"
forged_status="$(curl -sS --max-time 10 -o /tmp/org-selector-forged.html -w '%{http_code}' \
  -c "${jar}" -b "${jar}" --data-urlencode "organization=" "${selection_action}")"
if [[ "${forged_status}" == "403" ]]; then
  pass "a non-administrator cannot forge a tenant-wide submission"
else
  fail "a non-administrator cannot forge a tenant-wide submission (HTTP ${forged_status})"
fi
if grep -qF "only available to an administrator" /tmp/org-selector-forged.html; then
  pass "the rejection names an explicit reason"
else
  fail "the rejection names an explicit reason (page did not contain it)"
fi
rm -f "${jar}" /tmp/org-selector-forged.html

echo
echo "--- a member of more than one organization sees the selection screen (issue #80) ---"

# The credentials POST's own response body is the selection screen: a
# member of more than one organization reaches it directly, no separate
# navigation involved.
jar="$(mktemp)"
login_page tenant-a patient-app "${jar}"
action="$(login_action "${login_body}")"
form_body="$(curl -sS --max-time 10 -c "${jar}" -b "${jar}" \
  --data-urlencode "username=user-doctor-multi" --data-urlencode "password=demo-password" \
  "${action}")"
listed_aliases="$(grep -o 'value="[a-z-]*hospital"' <<<"${form_body}" | sed -e 's/value="//' -e 's/"$//' | sort)"
if [[ "${listed_aliases}" == $'north-hospital\nsouth-hospital' ]]; then
  pass "the screen lists exactly the caller's own organizations"
else
  fail "the screen lists exactly the caller's own organizations (was: ${listed_aliases})"
fi
selection_action="$(login_action "${form_body}")"

echo
echo "--- each answer yields a token whose active hospital differs accordingly ---"

submit_organization "${selection_action}" "${jar}" "south-hospital"
code="$(code_from_redirect)"
south_organization="absent"
if [[ -n "${code}" ]]; then
  response="$(token_from_code tenant-a patient-app "${code}")"
  access_token="$(jq -r '.access_token // empty' <<<"${response}")"
  south_organization="$(claim_of "${access_token}" '.organization | tojson')"
fi

jar="$(mktemp)"
login_page tenant-a patient-app "${jar}"
action="$(login_action "${login_body}")"
form_body="$(curl -sS --max-time 10 -c "${jar}" -b "${jar}" \
  --data-urlencode "username=user-doctor-multi" --data-urlencode "password=demo-password" \
  "${action}")"
selection_action="$(login_action "${form_body}")"
submit_organization "${selection_action}" "${jar}" "north-hospital"
code="$(code_from_redirect)"
north_organization="absent"
if [[ -n "${code}" ]]; then
  response="$(token_from_code tenant-a patient-app "${code}")"
  access_token="$(jq -r '.access_token // empty' <<<"${response}")"
  north_organization="$(claim_of "${access_token}" '.organization | tojson')"
fi

if [[ "${south_organization}" == '["south-hospital"]' && "${north_organization}" == '["north-hospital"]' ]]; then
  pass "the same user's two answers yield two different active hospitals"
else
  fail "the same user's two answers yield two different active hospitals (south answer -> ${south_organization}, north answer -> ${north_organization})"
fi
rm -f "${jar}"

echo
echo "--- a submitted organization the user is not a member of is rejected ---"

jar="$(mktemp)"
login_page tenant-a patient-app "${jar}"
action="$(login_action "${login_body}")"
form_body="$(curl -sS --max-time 10 -c "${jar}" -b "${jar}" \
  --data-urlencode "username=user-doctor-multi" --data-urlencode "password=demo-password" \
  "${action}")"
selection_action="$(login_action "${form_body}")"
tampered_status="$(curl -sS --max-time 10 -o /tmp/org-selector-tampered.html -w '%{http_code}' \
  -c "${jar}" -b "${jar}" --data-urlencode "organization=a-hospital-nobody-belongs-to" "${selection_action}")"
if [[ "${tampered_status}" == "403" ]]; then
  pass "a submitted organization the user is not a member of is rejected, not honoured"
else
  fail "a submitted organization the user is not a member of is rejected (HTTP ${tampered_status})"
fi
if grep -qF "not one you belong to" /tmp/org-selector-tampered.html; then
  pass "the rejection names an explicit reason"
else
  fail "the rejection names an explicit reason (page did not contain it)"
fi
rm -f "${jar}" /tmp/org-selector-submission.html /tmp/org-selector-tampered.html

if (( failures > 0 )); then
  echo
  echo "${failures} organization-selector failure(s)"
  exit 1
fi

echo
echo "organization selector end to end passed"
