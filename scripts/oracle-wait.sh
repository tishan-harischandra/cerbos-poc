#!/usr/bin/env bash
# Waits for Oracle to be ready to accept a migration.
#
# The image ships its pluggable database already created and the compose service
# mounts no volume, so this usually returns within seconds. The generous budget is
# for the cases that are not usual: a cold image layer cache, a loaded CI runner,
# or a future switch to a persisted data directory. Getting this wrong in the
# impatient direction reports a broken engine when it is merely still starting.
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
  # healthcheck.sh ships with the image and queries the pluggable database rather
  # than merely probing the listener, so it does not pass before the schema is
  # usable. That distinction is the whole point: a listener answers long before
  # the database behind it will accept a migration.
  if ${DOCKER:-docker} exec "${container}" healthcheck.sh >/dev/null 2>&1; then
    echo "oracle-wait: Oracle is ready after ${SECONDS}s"
    exit 0
  fi
  sleep 5
done

echo "oracle-wait: Oracle did not become ready within ${TIMEOUT_SECONDS}s" >&2
${DOCKER:-docker} logs --tail 30 "${container}" >&2 2>/dev/null || true
exit 1
