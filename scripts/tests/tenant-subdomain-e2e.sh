#!/usr/bin/env bash
# Subdomain tenant resolution, end to end (issue #83): a user reaches their
# hospital group by its own web address, resolved from the request host
# rather than a value baked into either front end's bundle.
#
# Wildcard DNS that resolves to the loopback address (*.localtest.me) is what
# makes this work with no host-file editing: tenant-a.localtest.me and
# tenant-b.localtest.me both resolve to 127.0.0.1, and the only thing that
# differs between them is the Host header curl (or a browser) sends.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."

failures=0
pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }

command -v jq >/dev/null 2>&1 || { echo "tenant-subdomain-e2e: jq is required" >&2; exit 1; }

ADMIN_CONSOLE_PORT="${ADMIN_CONSOLE_PORT:-4200}"
BUSINESS_UI_PORT="${BUSINESS_UI_PORT:-4201}"

# env_js <subdomain-host> <port>
# Echoes the body of /assets/env.js as fetched from host:port.
env_js() {
  curl -sS --max-time 10 "http://$1:$2/assets/env.js"
}

# env_js_json <subdomain-host> <port>
# Echoes /assets/env.js's own configuration object as plain JSON: the
# response itself is a script assigning it to window.__ENV__, not a JSON
# document on its own.
env_js_json() {
  env_js "$1" "$2" | sed -e 's/^window\.__ENV__ = //' -e 's/;[[:space:]]*$//'
}

# env_js_status <subdomain-host> <port>
env_js_status() {
  curl -sS --max-time 10 -o /dev/null -w '%{http_code}' "http://$1:$2/assets/env.js"
}

echo "--- neither front end contains a baked-in issuer or client id ---"

admin_a="$(env_js tenant-a.localtest.me "${ADMIN_CONSOLE_PORT}")"
admin_b="$(env_js tenant-b.localtest.me "${ADMIN_CONSOLE_PORT}")"
if [[ "${admin_a}" == *"tenant-a"* && "${admin_b}" == *"tenant-b"* && "${admin_a}" != "${admin_b}" ]]; then
  pass "the Admin Console's runtime environment resolves per tenant from the host"
else
  fail "the Admin Console's runtime environment resolves per tenant from the host (tenant-a: ${admin_a}, tenant-b: ${admin_b})"
fi

business_a="$(env_js tenant-a.localtest.me "${BUSINESS_UI_PORT}")"
business_b="$(env_js tenant-b.localtest.me "${BUSINESS_UI_PORT}")"
if [[ "${business_a}" == *"tenant-a"* && "${business_b}" == *"tenant-b"* && "${business_a}" != "${business_b}" ]]; then
  pass "business-ui's runtime environment resolves per tenant from the host"
else
  fail "business-ui's runtime environment resolves per tenant from the host (tenant-a: ${business_a}, tenant-b: ${business_b})"
fi

echo
echo "--- an unknown tenant subdomain produces a clear error ---"

for port in "${ADMIN_CONSOLE_PORT}" "${BUSINESS_UI_PORT}"; do
  status="$(env_js_status tenant-nonexistent.localtest.me "${port}")"
  body="$(env_js tenant-nonexistent.localtest.me "${port}")"
  if [[ "${status}" == "404" && "${body}" != *"window.__ENV__"* ]]; then
    pass "port ${port}: an unknown tenant subdomain is refused with a clear error, not a JavaScript blob"
  else
    fail "port ${port}: an unknown tenant subdomain produces a clear error (status ${status}, body: ${body})"
  fi
done

echo
echo "--- logging in at two different tenant subdomains authenticates against two different realms ---"

# The resolved issuer, not an assumption about what it should be: this is
# the same value the browser would actually use.
admin_a_json="$(env_js_json tenant-a.localtest.me "${ADMIN_CONSOLE_PORT}")"
admin_b_json="$(env_js_json tenant-b.localtest.me "${ADMIN_CONSOLE_PORT}")"
issuer_a="$(jq -r '.oidcIssuer' <<<"${admin_a_json}")"
issuer_b="$(jq -r '.oidcIssuer' <<<"${admin_b_json}")"
client_a="$(jq -r '.oidcClientId' <<<"${admin_a_json}")"
client_b="$(jq -r '.oidcClientId' <<<"${admin_b_json}")"

if [[ -z "${issuer_a}" || "${issuer_a}" == "null" || -z "${issuer_b}" || "${issuer_b}" == "null" ]]; then
  fail "both subdomains resolved a real issuer to log in against (a: '${issuer_a}', b: '${issuer_b}')"
elif [[ "${issuer_a}" == "${issuer_b}" ]]; then
  fail "the two subdomains resolved to two different realms (both resolved to ${issuer_a})"
else
  token_a="$(curl -sS --max-time 10 -d "grant_type=password" -d "client_id=${client_a}" \
    -d "username=user-doctor" -d "password=demo-password" "${issuer_a}/protocol/openid-connect/token" \
    | jq -r '.access_token // empty')"
  token_b="$(curl -sS --max-time 10 -d "grant_type=password" -d "client_id=${client_b}" \
    -d "username=user-doctor-b" -d "password=demo-password" "${issuer_b}/protocol/openid-connect/token" \
    | jq -r '.access_token // empty')"

  if [[ -n "${token_a}" && -n "${token_b}" ]]; then
    pass "a real login against each subdomain's resolved realm succeeds, and the two realms differ"
  else
    fail "a real login against each subdomain's resolved realm succeeds (tenant-a token present: $([[ -n "${token_a}" ]] && echo yes || echo no), tenant-b token present: $([[ -n "${token_b}" ]] && echo yes || echo no))"
  fi
fi

if (( failures > 0 )); then
  echo
  echo "${failures} tenant-subdomain failure(s)"
  exit 1
fi

echo
echo "subdomain tenant resolution end to end passed"
