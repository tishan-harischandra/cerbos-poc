#!/usr/bin/env bash
# Runs the assignmentstore contract suite against real engines.
#
#   scripts/tests/store-contract.sh <postgres|oracle|dual>
#
# The suite itself is one set of dialect-agnostic assertions; this script only
# decides which engines it is pointed at. "dual" is the mode that makes the
# portability claim: the same assertions, both engines, in one run.
#
# Neither database is published to the host, so the tests run inside the compose
# network rather than reaching in from outside.
set -uo pipefail

mode="${1:-postgres}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ -f "${repo_root}/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "${repo_root}/.env"
  set +a
fi

export GO_NETWORK="${GO_NETWORK:-${COMPOSE_PROJECT_NAME:-cerbos-poc}_default}"

postgres_dsn="postgres://${POSTGRES_USER:-cerbos_poc}:${POSTGRES_PASSWORD:-change-me}@${POSTGRES_HOST:-postgres}:${POSTGRES_PORT:-5432}/${POSTGRES_DB:-cerbos_poc}?sslmode=disable"
oracle_dsn="oracle://${ORACLE_USER:-cerbos_poc}:${ORACLE_PASSWORD:-change-me}@${ORACLE_HOST:-oracle}:${ORACLE_PORT:-1521}/${ORACLE_SERVICE:-FREEPDB1}"

case "${mode}" in
  postgres)
    export ASSIGNMENTSTORE_POSTGRES_DSN="${postgres_dsn}"
    export REQUIRE_ENGINES="postgres"
    ;;
  oracle)
    export ASSIGNMENTSTORE_ORACLE_DSN="${oracle_dsn}"
    export REQUIRE_ENGINES="oracle"
    ;;
  dual)
    export ASSIGNMENTSTORE_POSTGRES_DSN="${postgres_dsn}"
    export ASSIGNMENTSTORE_ORACLE_DSN="${oracle_dsn}"
    # REQUIRE_ENGINES turns a skipped engine into a failure. A skip and a pass
    # look identical in a CI summary, and the portability claim rests entirely on
    # this suite having actually run against both.
    export REQUIRE_ENGINES="postgres,oracle"
    ;;
  *)
    echo "store-contract: unknown mode '${mode}'" >&2
    exit 64
    ;;
esac

export GO_ENV_PASS="ASSIGNMENTSTORE_POSTGRES_DSN ASSIGNMENTSTORE_ORACLE_DSN REQUIRE_ENGINES"

echo "--- the store contract on ${mode} ---"
exec bash "${repo_root}/scripts/go.sh" libs/assignmentstore test -count=1 -v ./...
