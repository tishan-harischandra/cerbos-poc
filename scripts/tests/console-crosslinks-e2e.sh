#!/usr/bin/env bash
# Console cross-links and deep-link survival through login (issue #82): the
# organization selector must never rewrite or override redirect_uri, proven
# for a deep link into each console - the Admin Console's own client
# (patient-app) and Keycloak's own administration console client
# (security-admin-console) - the same way as the existing identity suites,
# with curl and a cookie jar rather than a browser automation toolchain.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
# shellcheck source=scripts/tests/lib-token.sh
source scripts/tests/lib-token.sh

failures=0
pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }

command -v jq >/dev/null 2>&1 || { echo "console-crosslinks-e2e: jq is required" >&2; exit 1; }
wait_for_keycloak || exit 1

# Discovery's own issuer names the host Keycloak actually issues cookies
# for (see org-selector-e2e.sh's comment on the same fix).
BASE_URL="$(curl -sS --max-time 10 "${KEYCLOAK_URL}/realms/tenant-a/.well-known/openid-configuration" \
  | jq -r '.issuer' | sed 's#/realms/tenant-a$##')"
if [[ -z "${BASE_URL}" || "${BASE_URL}" == "null" ]]; then
  echo "console-crosslinks-e2e: could not determine Keycloak's own hostname from discovery" >&2
  exit 1
fi

# pkce_pair
# Echoes "<verifier> <challenge>" for an S256 PKCE pair - only
# security-admin-console, Keycloak's own built-in client, requires one.
pkce_pair() {
  python3 - <<'PY'
import base64
import hashlib
import secrets

verifier = base64.urlsafe_b64encode(secrets.token_bytes(32)).rstrip(b"=").decode()
challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
print(verifier, challenge)
PY
}

# assert_deep_link_survives <description> <client-id> <redirect-uri> [extra curl args...]
# Logs user-doctor (a single-membership, no-screen login) in against
# client-id with redirect-uri, and asserts the flow's final redirect lands
# on exactly that URI - the organization selector's own logic never runs
# a second time between the login form and the redirect, so this is a
# direct proof it never touches redirect_uri.
assert_deep_link_survives() {
  local description="$1" client="$2" redirect_uri="$3"
  shift 3

  local jar login_body action login_headers location
  jar="$(mktemp)"
  login_body="$(curl -sS --max-time 10 -c "${jar}" \
    --get "${BASE_URL}/realms/tenant-a/protocol/openid-connect/auth" \
    --data-urlencode "client_id=${client}" \
    --data-urlencode "response_type=code" \
    --data-urlencode "scope=openid" \
    --data-urlencode "redirect_uri=${redirect_uri}" \
    "$@")"
  action="$(grep -o 'action="[^"]*"' <<<"${login_body}" | head -1 \
    | sed -e 's/action="//' -e 's/"$//' -e 's/&amp;/\&/g')"
  login_headers="$(curl -sS --max-time 10 -D - -o /dev/null -c "${jar}" -b "${jar}" \
    --data-urlencode "username=user-doctor" \
    --data-urlencode "password=demo-password" \
    "${action}")"
  rm -f "${jar}"

  location="$(grep -i '^location:' <<<"${login_headers}" | sed -e 's/^[Ll]ocation: *//' -e 's/\r$//')"
  if [[ "${location}" == "${redirect_uri}"\?* ]]; then
    pass "${description}"
  else
    fail "${description} (landed on '${location}', want a query appended to exactly '${redirect_uri}')"
  fi
}

echo "--- a deep link into the Admin Console survives a full login ---"
assert_deep_link_survives \
  "a deep link into the Admin Console lands on the exact path requested" \
  patient-app "http://127.0.0.1:4200/role-matrix"

echo
echo "--- a deep link into Keycloak's own administration console survives a full login ---"
read -r verifier challenge <<<"$(pkce_pair)"
assert_deep_link_survives \
  "a deep link into Keycloak's own administration console lands on the exact path requested" \
  security-admin-console "${BASE_URL}/admin/tenant-a/console/" \
  --data-urlencode "code_challenge=${challenge}" \
  --data-urlencode "code_challenge_method=S256"

echo
echo "--- navigating between the two consoles requires no re-entry of credentials ---"
jar="$(mktemp)"
login_body="$(curl -sS --max-time 10 -c "${jar}" \
  --get "${BASE_URL}/realms/tenant-a/protocol/openid-connect/auth" \
  --data-urlencode "client_id=patient-app" \
  --data-urlencode "response_type=code" \
  --data-urlencode "scope=openid" \
  --data-urlencode "redirect_uri=http://127.0.0.1:4200/")"
action="$(grep -o 'action="[^"]*"' <<<"${login_body}" | head -1 \
  | sed -e 's/action="//' -e 's/"$//' -e 's/&amp;/\&/g')"
first_headers="$(curl -sS --max-time 10 -D - -o /dev/null -c "${jar}" -b "${jar}" \
  --data-urlencode "username=user-doctor" --data-urlencode "password=demo-password" \
  "${action}")"
first_session="$(grep -i '^location:' <<<"${first_headers}" | grep -o 'session_state=[^&]*' | cut -d= -f2)"

read -r verifier challenge <<<"$(pkce_pair)"
# The same cookie jar, a different client, no credentials submitted at
# all: a plain GET against the authorization endpoint either redirects
# straight through (the SSO session already covers it) or shows a login
# form (it does not).
second_headers="$(curl -sS --max-time 10 -D - -o /tmp/console-crosslinks-second.html \
  -c "${jar}" -b "${jar}" --get "${BASE_URL}/realms/tenant-a/protocol/openid-connect/auth" \
  --data-urlencode "client_id=security-admin-console" \
  --data-urlencode "response_type=code" \
  --data-urlencode "scope=openid" \
  --data-urlencode "redirect_uri=${BASE_URL}/admin/tenant-a/console/" \
  --data-urlencode "code_challenge=${challenge}" \
  --data-urlencode "code_challenge_method=S256")"
rm -f "${jar}" /tmp/console-crosslinks-second.html
second_status="$(head -1 <<<"${second_headers}" | grep -o '[0-9][0-9][0-9]')"
second_session="$(grep -i '^location:' <<<"${second_headers}" | grep -o 'session_state=[^&]*' | cut -d= -f2)"

if [[ "${second_status}" == "302" && -n "${first_session}" && "${first_session}" == "${second_session}" ]]; then
  pass "moving to the other console redirects straight through the same SSO session, no credentials"
else
  fail "moving to the other console requires no re-entry of credentials (status ${second_status}, sessions '${first_session}' vs '${second_session}')"
fi

echo
echo "--- the login theme offers a route back to the Admin Console ---"
jar="$(mktemp)"
login_body="$(curl -sS --max-time 10 -c "${jar}" \
  --get "${BASE_URL}/realms/tenant-a/protocol/openid-connect/auth" \
  --data-urlencode "client_id=patient-app" \
  --data-urlencode "response_type=code" \
  --data-urlencode "scope=openid" \
  --data-urlencode "redirect_uri=http://127.0.0.1:4200/")"
action="$(grep -o 'action="[^"]*"' <<<"${login_body}" | head -1 \
  | sed -e 's/action="//' -e 's/"$//' -e 's/&amp;/\&/g')"
# user-doctor-multi's screen (issue #80), the one page this theme actually
# overrides, is where the link lives.
form_body="$(curl -sS --max-time 10 -c "${jar}" -b "${jar}" \
  --data-urlencode "username=user-doctor-multi" --data-urlencode "password=demo-password" \
  "${action}")"
rm -f "${jar}"
if grep -qF 'id="kc-back-to-admin-console"' <<<"${form_body}"; then
  pass "the login theme offers a route back to the Admin Console"
else
  fail "the login theme offers a route back to the Admin Console (page did not contain it)"
fi

if (( failures > 0 )); then
  echo
  echo "${failures} console-crosslinks failure(s)"
  exit 1
fi

echo
echo "console cross-links and deep-link survival passed"
