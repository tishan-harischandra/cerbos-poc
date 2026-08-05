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

body="$(curl -fsS "${BASE}/index.html" 2>/dev/null)"
check "the admin console is served on ${BASE}" "$?"

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
  "http://127.0.0.1:${KEYCLOAK_PORT:-8081}/realms/${IDP_REALM:-cerbos-poc}/.well-known/openid-configuration" 2>/dev/null)"
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

# nginx resolves a literal proxy_pass hostname once at startup, so without a
# resolver the console proxies to a stale address the moment the ADS container is
# recreated, and every decision request comes back 502. Assert the rendered
# config still resolves at request time.
# Ask compose for the container rather than guessing its name: Docker Compose
# names it cerbos-poc-admin-console-1 and podman-compose cerbos-poc_admin-console_1.
console="$(docker compose ps -q admin-console 2>/dev/null | head -1)"
rendered=""
if [[ -n "${console}" ]]; then
  rendered="$(docker exec "${console}" nginx -T 2>/dev/null)"
fi
[[ -n "${rendered}" ]]
check "the console's nginx config can be read" "$?"
grep -q 'resolver ' <<<"${rendered}"
check "the console proxy configures a DNS resolver" "$?"
grep -qE 'proxy_pass \$' <<<"${rendered}"
check "the console proxy resolves the ADS at request time, surviving a rebuild" "$?"

if (( failures > 0 )); then
  echo
  echo "${failures} smoke failure(s)"
  exit 1
fi

echo
echo "stack smoke passed"
