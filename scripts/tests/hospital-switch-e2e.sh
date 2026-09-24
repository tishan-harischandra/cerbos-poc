#!/usr/bin/env bash
# The hospital switcher, end to end (issue #84), against a real `make up`
# stack. It proves that the custom browser flow retains Keycloak's native
# Organization branch: an existing SSO session can complete a prompt=none
# Authorization Code + PKCE transition for another real membership, while
# a request for a non-membership is refused.
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
  local code="$1" verifier="${2:-}"
  local args=(
    -d "grant_type=authorization_code"
    -d "client_id=patient-app"
    -d "code=${code}"
    -d "redirect_uri=${REDIRECT_URI}"
  )
  [[ -n "${verifier}" ]] && args+=(-d "code_verifier=${verifier}")
  curl -sS --max-time 10 "${args[@]}" \
    "${KEYCLOAK_URL}/realms/tenant-a/protocol/openid-connect/token"
}

pkce_pair() {
  python3 - <<'PY'
import base64
import hashlib
import secrets
verifier = secrets.token_urlsafe(48)
challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
print(verifier, challenge)
PY
}

# login <cookie-jar> <scope>
# Runs a full authorization + credentials submission, echoing the access
# token, or an empty string on failure.
login() {
  local jar="$1" scope="$2"
  authorize "${jar}" "${scope}"
  local action; action="$(login_action "${authorize_body}")"
  local headers
  headers="$(curl -sS --max-time 10 -D - -o /tmp/hospital-switch-login.html -c "${jar}" -b "${jar}" \
    --data-urlencode "username=user-doctor-multi" --data-urlencode "password=demo-password" \
    "${action}")"
  desecure_cookie_jar "${jar}"
  local code; code="$(code_from_headers "${headers}")"
  if [[ -z "${code}" ]] && grep -qF 'name="password"' /tmp/hospital-switch-login.html; then
    local password_action
    password_action="$(login_action "$(cat /tmp/hospital-switch-login.html)")"
    headers="$(curl -sS --max-time 10 -D - -o /tmp/hospital-switch-login.html -c "${jar}" -b "${jar}" \
      --data-urlencode "password=demo-password" "${password_action}")"
    desecure_cookie_jar "${jar}"
    code="$(code_from_headers "${headers}")"
  fi
  if [[ -z "${code}" ]]; then
    local selection_action
    selection_action="$(login_action "$(cat /tmp/hospital-switch-login.html)")"
    headers="$(curl -sS --max-time 10 -D - -o /dev/null -c "${jar}" -b "${jar}" \
      --data-urlencode "organization=north-hospital" "${selection_action}")"
    desecure_cookie_jar "${jar}"
    code="$(code_from_headers "${headers}")"
  fi
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
echo "--- a prompt=none switch reuses SSO and yields a code for the target hospital ---"

read -r switch_verifier switch_challenge <<<"$(pkce_pair)"
authorize "${jar}" "openid organization:south-hospital" \
  --data-urlencode "prompt=none" \
  --data-urlencode "code_challenge=${switch_challenge}" \
  --data-urlencode "code_challenge_method=S256"
switch_error="$(error_from_headers "${authorize_headers}")"
switch_code="$(code_from_headers "${authorize_headers}")"
if [[ -n "${switch_code}" && -z "${switch_error}" ]]; then
  pass "an existing SSO session switches hospitals without an interactive screen"
else
  fail "an existing SSO session switches hospitals without an interactive screen (error=${switch_error}, code=${switch_code})"
fi

switched_token=""
if [[ -n "${switch_code}" ]]; then
  switched_token="$(jq -r '.access_token // empty' <<<"$(token_from_code "${switch_code}" "${switch_verifier}")")"
fi
switched_organization="$(claim_of "${switched_token}" '.organization | tojson' 2>/dev/null)"
if [[ "${switched_organization}" == '["south-hospital"]' ]]; then
  pass "the switched token names exactly the requested hospital"
else
  fail "the switched token names exactly the requested hospital (was ${switched_organization})"
fi

echo
echo "--- a switch to an organization the user is not a member of fails outright ---"

read -r rejected_verifier rejected_challenge <<<"$(pkce_pair)"
authorize "${jar}" "openid organization:a-hospital-nobody-belongs-to" \
  --data-urlencode "prompt=none" \
  --data-urlencode "code_challenge=${rejected_challenge}" \
  --data-urlencode "code_challenge_method=S256"
tampered_error="$(error_from_headers "${authorize_headers}")"
tampered_code="$(code_from_headers "${authorize_headers}")"
if [[ -n "${tampered_error}" && -z "${tampered_code}" ]]; then
  pass "a switch to an organization the user does not belong to reaches an error, never a code"
else
  fail "a switch to an organization the user does not belong to reaches an error, never a code (error=${tampered_error}, code=${tampered_code})"
fi

rm -f "${jar}" /tmp/hospital-switch-response.html /tmp/hospital-switch-login.html

if (( failures > 0 )); then
  echo
  echo "${failures} hospital-switch failure(s)"
  exit 1
fi

echo
echo "hospital switch end to end passed"
