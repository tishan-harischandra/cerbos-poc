#!/usr/bin/env bash
# Runs the leader election contract suite against real backends.
#
#   scripts/tests/leaderlock-contract.sh <postgres|oracle|dual|redis|all>
#
# The suite itself is one set of backend-agnostic assertions
# (libs/leaderlock/leaderlockcontract); this script only decides which
# backends it is pointed at. "dual" is the mode that makes ADR-009's
# portability claim for LEADER_ELECTION_TYPE=DATABASE: the same assertions,
# both engines, in one run.
#
# SINGLE and K8S_LEASE are not here. They need no infrastructure - K8S_LEASE
# runs its whole contract against a fake API server in its own package - so
# `go test ./libs/leaderlock/...` already covers them offline.
#
# Neither database is published to the host, so the tests run inside the
# compose network rather than reaching in from outside.
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
redis_addr="${LEADER_ELECTION_REDIS_ADDR:-redis:6379}"

# The mode decides which backends the suite is pointed at, and nothing else
# may. Sourcing .env above is what gives the addresses their values, but it
# also exports the very variables the tests read to decide whether to run, so
# a mode that deliberately left one out still inherited it.
#
# That is not hypothetical: .env.example carries LEADER_ELECTION_REDIS_ADDR
# for the redis profile, so `dual` - which starts PostgreSQL and Oracle and no
# Redis - ran the Redis contract anyway and spent 100 seconds timing out
# against nothing. Unsetting here makes the case below the only thing that can
# turn a backend on.
unset ASSIGNMENTSTORE_POSTGRES_DSN ASSIGNMENTSTORE_ORACLE_DSN LEADER_ELECTION_REDIS_ADDR

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
    # REQUIRE_ENGINES turns a skipped engine into a failure. A skip and a
    # pass look identical in a CI summary, and the portability claim rests
    # entirely on this suite having actually run against both.
    export REQUIRE_ENGINES="postgres,oracle"
    ;;
  redis)
    export LEADER_ELECTION_REDIS_ADDR="${redis_addr}"
    ;;
  all)
    export ASSIGNMENTSTORE_POSTGRES_DSN="${postgres_dsn}"
    export ASSIGNMENTSTORE_ORACLE_DSN="${oracle_dsn}"
    export LEADER_ELECTION_REDIS_ADDR="${redis_addr}"
    export REQUIRE_ENGINES="postgres,oracle"
    ;;
  *)
    echo "leaderlock-contract: unknown mode '${mode}'" >&2
    exit 64
    ;;
esac

# Every adapter must build with cgo disabled, for the same reason the store
# adapters must: no native client belongs in any image. This fails the moment
# somebody reaches for a driver that needs one.
echo "--- the adapters need no native client ---"
if CGO_ENABLED=0 GO_ENV_PASS="CGO_ENABLED" \
    bash "${repo_root}/scripts/go.sh" libs/leaderlock build ./... >/tmp/leaderlock-cgo.log 2>&1; then
  echo "ok   every adapter builds as pure Go"
else
  echo "FAIL every adapter builds as pure Go"
  tail -10 /tmp/leaderlock-cgo.log
  exit 1
fi

export GO_ENV_PASS="ASSIGNMENTSTORE_POSTGRES_DSN ASSIGNMENTSTORE_ORACLE_DSN LEADER_ELECTION_REDIS_ADDR LEADER_ELECTION_REDIS_PASSWORD REQUIRE_ENGINES"

echo
echo "--- the leader election contract on ${mode} ---"
# -timeout: the contract deliberately waits out real lease expiries on every
# adapter, so the default 10 minutes is not generous once several backends
# run in one invocation.
exec bash "${repo_root}/scripts/go.sh" libs/leaderlock test -count=1 -timeout 20m -v ./...
