#!/usr/bin/env bash
# Writes the demo role matrix into the authorization database.
#
#   scripts/seed.sh [postgres]
#
# The database is not published to the host, so the seeder runs inside the
# compose network, the same way the store contract suite reaches it.
#
# Safe to re-run: every row is written on its §8.2 unique key.
set -euo pipefail

engine="${1:-postgres}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -f "${repo_root}/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "${repo_root}/.env"
  set +a
fi

case "${engine}" in
  postgres) ;;
  *)
    echo "scripts/seed.sh: unknown engine '${engine}'" >&2
    exit 64
    ;;
esac

export GO_NETWORK="${GO_NETWORK:-${COMPOSE_PROJECT_NAME:-cerbos-poc}_default}"
export ASSIGNMENTSTORE_POSTGRES_DSN="postgres://${POSTGRES_USER:-cerbos_poc}:${POSTGRES_PASSWORD:-change-me}@${POSTGRES_HOST:-postgres}:${POSTGRES_PORT:-5432}/${POSTGRES_DB:-cerbos_poc}?sslmode=disable"
export GO_ENV_PASS="ASSIGNMENTSTORE_POSTGRES_DSN"

exec bash "${repo_root}/scripts/go.sh" libs/assignmentstore run ./cmd/seed
