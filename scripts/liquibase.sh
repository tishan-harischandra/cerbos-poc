#!/usr/bin/env bash
# Runs Liquibase in a container so no JVM is needed on the host or in any
# service image.
#
#   scripts/liquibase.sh <engine> <liquibase-command> [args...]
#
# engine is postgres or oracle. The engine only selects the connection URL and
# the JDBC driver; the changelog is the same set either way, which is the whole
# portability claim.
#
# Examples:
#   scripts/liquibase.sh postgres update
#   scripts/liquibase.sh postgres status --verbose
#   scripts/liquibase.sh oracle update
set -euo pipefail

DOCKER="${DOCKER:-docker}"
LIQUIBASE_IMAGE="${LIQUIBASE_IMAGE:-docker.io/liquibase/liquibase:4.31-alpine}"
NETWORK="${LIQUIBASE_NETWORK:-cerbos-poc_default}"

if [[ $# -lt 2 ]]; then
  echo "usage: scripts/liquibase.sh <postgres|oracle> <liquibase-command> [args...]" >&2
  exit 64
fi

engine="$1"
shift

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# .env carries the credentials the compose services were started with, so the
# migration and the database agree without repeating them here.
if [[ -f "${repo_root}/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "${repo_root}/.env"
  set +a
fi

case "${engine}" in
  postgres)
    host="${POSTGRES_HOST:-postgres}"
    port="${POSTGRES_PORT:-5432}"
    database="${POSTGRES_DB:-cerbos_poc}"
    url="jdbc:postgresql://${host}:${port}/${database}"
    username="${POSTGRES_USER:-cerbos_poc}"
    password="${POSTGRES_PASSWORD:-change-me}"
    ;;
  oracle)
    host="${ORACLE_HOST:-oracle}"
    port="${ORACLE_PORT:-1521}"
    service="${ORACLE_SERVICE:-FREEPDB1}"
    url="jdbc:oracle:thin:@//${host}:${port}/${service}"
    username="${ORACLE_USER:-cerbos_poc}"
    password="${ORACLE_PASSWORD:-change-me}"
    ;;
  *)
    echo "scripts/liquibase.sh: unknown engine '${engine}'" >&2
    exit 64
    ;;
esac

if ! command -v "${DOCKER}" >/dev/null 2>&1; then
  echo "scripts/liquibase.sh: '${DOCKER}' not found on PATH" >&2
  exit 127
fi

exec "${DOCKER}" run --rm \
  --network "${NETWORK}" \
  -v "${repo_root}/deploy/liquibase/changelog:/liquibase/changelog:ro,z" \
  "${LIQUIBASE_IMAGE}" \
  --url="${url}" \
  --username="${username}" \
  --password="${password}" \
  --changeLogFile=db.changelog-master.yaml \
  --searchPath=/liquibase/changelog \
  --logLevel="${LIQUIBASE_LOG_LEVEL:-info}" \
  "$@"
