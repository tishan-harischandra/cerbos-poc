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

! curl -fsS --max-time 3 "http://127.0.0.1:3592/_cerbos/health" >/dev/null 2>&1
check "the Cerbos PDP is not published to the host" "$?"

if (( failures > 0 )); then
  echo
  echo "${failures} smoke failure(s)"
  exit 1
fi

echo
echo "stack smoke passed"
