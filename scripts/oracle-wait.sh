#!/usr/bin/env bash
# Waits for Oracle to be ready to accept a migration.
#
# Oracle's first start creates the pluggable database and the application user,
# which takes minutes on a cold volume. Every caller of this script would
# otherwise reimplement the same wait, and a short one reports a broken engine
# when it is merely still starting.
set -uo pipefail

COMPOSE="${COMPOSE:-docker compose}"
TIMEOUT_SECONDS="${ORACLE_WAIT_TIMEOUT:-900}"

container="$(${COMPOSE} ps -q oracle 2>/dev/null | head -1)"
if [[ -z "${container}" ]]; then
  echo "oracle-wait: the oracle service is not running; start it with --profile oracle" >&2
  exit 1
fi

deadline=$((SECONDS + TIMEOUT_SECONDS))
echo "oracle-wait: waiting up to ${TIMEOUT_SECONDS}s for Oracle to become healthy"

while (( SECONDS < deadline )); do
  # healthcheck.sh ships with the image and reports on the pluggable database,
  # not merely on the listener, so it does not pass before the schema is usable.
  if ${DOCKER:-docker} exec "${container}" healthcheck.sh >/dev/null 2>&1; then
    echo "oracle-wait: Oracle is ready after ${SECONDS}s"
    exit 0
  fi
  sleep 5
done

echo "oracle-wait: Oracle did not become ready within ${TIMEOUT_SECONDS}s" >&2
${DOCKER:-docker} logs --tail 30 "${container}" >&2 2>/dev/null || true
exit 1
