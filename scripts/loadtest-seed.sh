#!/usr/bin/env bash
# Writes the §15 load population into the loadtest profile's Keycloak and,
# if ASSIGNMENTSTORE_POSTGRES_DSN resolves, the authorization database.
#
#   scripts/loadtest-seed.sh [demo|load]
#
# demo is a small population (the same generator, small Config) useful for
# exercising the harness itself; load is the full 600,000 user / 42,000,000
# mapping population §15 specifies. Neither database is published to the
# host, so this runs inside the compose network like every other seed script.
set -euo pipefail

profile="${1:-demo}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "${repo_root}/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "${repo_root}/.env"
  set +a
fi

case "${profile}" in
  demo|load) ;;
  *)
    echo "scripts/loadtest-seed.sh: unknown profile '${profile}' (want demo or load)" >&2
    exit 64
    ;;
esac

export GO_NETWORK="${GO_NETWORK:-${COMPOSE_PROJECT_NAME:-tenant-a}_default}"
export LOADSEED_PROFILE="${profile}"
export KEYCLOAK_LOADTEST_ADMIN_URL="http://keycloak-loadtest:8080"
export KEYCLOAK_LOADTEST_DB_DSN="postgres://${KEYCLOAK_DB_USER:-keycloak}:${KEYCLOAK_DB_PASSWORD:-change-me}@keycloak-db:5432/${KEYCLOAK_DB_NAME:-keycloak}?sslmode=disable"
export KEYCLOAK_ADMIN="${KEYCLOAK_ADMIN:-admin}"
export KEYCLOAK_ADMIN_PASSWORD="${KEYCLOAK_ADMIN_PASSWORD:-change-me}"
export KEYCLOAK_LOADTEST_REALM="${KEYCLOAK_LOADTEST_REALM:-tenant-a-loadtest}"
export ASSIGNMENTSTORE_POSTGRES_DSN="postgres://${POSTGRES_USER:-cerbos_poc}:${POSTGRES_PASSWORD:-change-me}@${POSTGRES_HOST:-postgres}:${POSTGRES_PORT:-5432}/${POSTGRES_DB:-cerbos_poc}?sslmode=disable"
export LOADSEED_DATA_DIR="/workspace"
export LOADSEED_SKIP_KEYCLOAK="${LOADSEED_SKIP_KEYCLOAK:-}"
export GO_ENV_PASS="LOADSEED_PROFILE KEYCLOAK_LOADTEST_ADMIN_URL KEYCLOAK_LOADTEST_DB_DSN KEYCLOAK_ADMIN KEYCLOAK_ADMIN_PASSWORD KEYCLOAK_LOADTEST_REALM ASSIGNMENTSTORE_POSTGRES_DSN LOADSEED_DATA_DIR LOADSEED_SKIP_KEYCLOAK"

echo "--- starting the loadtest profile's keycloak (if not already running) ---"
docker compose --profile loadtest up --detach keycloak-db keycloak-loadtest
bash "${repo_root}/scripts/compose-wait.sh" keycloak-db keycloak-loadtest

echo
echo "--- loadseed (${profile} profile) ---"
exec bash "${repo_root}/scripts/go.sh" libs/keycloakbulkload/cmd/loadseed run .
