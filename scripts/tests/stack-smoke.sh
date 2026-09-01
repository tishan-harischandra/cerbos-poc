#!/usr/bin/env bash
# End-to-end checks against a running `make up` stack.
set -uo pipefail

PORT="${ADMIN_CONSOLE_PORT:-4200}"
BASE="http://127.0.0.1:${PORT}"
failures=0

check() {
  local description="$1" condition="$2"
  if [[ "${condition}" == "0" ]]; then
    echo "ok   ${description}"
  else
    echo "FAIL ${description}"
    failures=$((failures + 1))
  fi
}

# The console's own entry point rather than /index.html: a Go file server
# redirects the explicit file name to the directory, so asking for it would
# assert on a 301 instead of on the page.
body="$(curl -fsS "${BASE}/" 2>/dev/null)"
check "the admin console is served on ${BASE}" "$?"
grep -q '<app-root>' <<<"${body}"
check "the page served is the console's application shell" "$?"

health="$(curl -fsS "${BASE}/api/ads/healthz" 2>/dev/null)"
check "the ADS health endpoint answers through the console proxy" "$?"
[[ "${health}" == *'"status":"ok"'* ]]
check "the ADS reports itself alive" "$?"

ready="$(curl -fsS "${BASE}/api/ads/readyz" 2>/dev/null)"
check "the ADS readiness endpoint answers" "$?"
[[ "${ready}" == *'"cerbos":"ok"'* ]]
check "the ADS reaches Cerbos over gRPC from inside the network" "$?"
[[ "${ready}" == *'"postgres":"ok"'* ]]
check "the ADS reaches PostgreSQL from inside the network" "$?"
[[ "${ready}" == *'"idp":"ok"'* ]]
check "the ADS reaches the identity provider from inside the network" "$?"

# Unlike the PDP, Keycloak has to be reachable from the host: a login is a
# browser redirect, and a redirect nobody can follow is not a login.
discovery="$(curl -fsS --max-time 5 \
  "http://127.0.0.1:${KEYCLOAK_PORT:-8081}/realms/${IDP_REALM:-tenant-a}/.well-known/openid-configuration" 2>/dev/null)"
check "the identity provider is reachable from the browser's side" "$?"
grep -q 'authorization_endpoint' <<<"${discovery}"
check "the realm publishes an authorization endpoint to log in against" "$?"

! curl -fsS --max-time 3 "http://127.0.0.1:3592/_cerbos/health" >/dev/null 2>&1
check "the Cerbos PDP is not published to the host" "$?"

# The compose contract asserts Oracle declares a profile; this asserts the
# profile actually kept it out of the default stack, which is the property that
# matters to anyone running `make up`.
[[ -z "$(docker compose ps --status running --services 2>/dev/null | grep -x oracle)" ]]
check "oracle is not running in the default stack" "$?"

# ADR-008: the console is served by admin-service, so there is no console
# container to inspect. What replaced the nginx assertions is the behaviour
# they were standing in for, asked of the running stack directly.

# A deep link is the case a static file server gets wrong: the path is a route
# the browser's own router knows and no file exists for it.
deep_link_status="$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/user-overrides/tenant-a" 2>/dev/null)"
[[ "${deep_link_status}" == "200" ]]
check "a console deep link is answered by the application shell" "$?"

# The runtime environment resolves per tenant from the request's own Host
# header (issue #83) rather than from a value baked into the bundle, so a
# plain 127.0.0.1 request names no tenant subdomain and gets no environment
# to serve: this asks the same way a browser on tenant-a's own subdomain
# would, over wildcard DNS that resolves straight to the loopback address.
env_js="$(curl -fsS -H "Host: tenant-a.localtest.me" "${BASE}/assets/env.js" 2>/dev/null)"
check "the console's runtime environment is served" "$?"
grep -q 'oidcIssuer' <<<"${env_js}"
check "the runtime environment names the issuer to log in against" "$?"

# The issue #26 regression, asked of a live stack: every administration route
# is registered under /admin, and the console calls it under /api/admin. A 404
# here means the prefix did not survive the rewrite. 401 is the right answer -
# the route matched and the token check ran.
admin_status="$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/api/admin/authz/resources" 2>/dev/null)"
[[ "${admin_status}" != "404" ]]
check "the console's admin API calls reach a registered route" "$?"
[[ "${admin_status}" == "401" ]]
check "an unauthenticated admin API call is refused rather than served" "$?"

# A missing asset must not be answered by the shell: a bundle whose javascript
# 404s is obvious, one that returns HTML with a 200 is a blank page.
missing_status="$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/main-does-not-exist.js" 2>/dev/null)"
[[ "${missing_status}" == "404" ]]
check "a missing console asset is a 404 rather than the shell" "$?"

# The nginx proxy resolved its upstream once at startup, so recreating the ADS
# left it proxying to an address nobody answered on. The Go resolver looks the
# name up per request, so the console survives a rebuild of the ADS.
docker compose up --detach --force-recreate ads >/dev/null 2>&1
for _ in $(seq 1 30); do
  curl -fsS --max-time 2 "${BASE}/api/ads/healthz" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS --max-time 5 "${BASE}/api/ads/healthz" >/dev/null 2>&1
check "the ADS proxy still resolves after the ADS is recreated" "$?"

if (( failures > 0 )); then
  echo
  echo "${failures} smoke failure(s)"
  exit 1
fi

echo
echo "stack smoke passed"
