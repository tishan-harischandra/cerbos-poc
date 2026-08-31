#!/usr/bin/env bash
# Writes the tenant registry file's entries into the authorization database
# (issue #76).
#
#   scripts/seed-tenants.sh [postgres]
#
# The database is not published to the host, so the seeder runs inside the
# compose network, the same way scripts/seed.sh reaches it.
#
# Safe to re-run: SaveTenant upserts on the realm's primary key.
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
    echo "scripts/seed-tenants.sh: unknown engine '${engine}'" >&2
    exit 64
    ;;
esac

export GO_NETWORK="${GO_NETWORK:-${COMPOSE_PROJECT_NAME:-cerbos-poc}_default}"
export ASSIGNMENTSTORE_POSTGRES_DSN="postgres://${POSTGRES_USER:-cerbos_poc}:${POSTGRES_PASSWORD:-change-me}@${POSTGRES_HOST:-postgres}:${POSTGRES_PORT:-5432}/${POSTGRES_DB:-cerbos_poc}?sslmode=disable"
export TENANT_REGISTRY_FILE="/workspace/deploy/tenant-registry.yaml"
export TENANT_ISSUER="${KEYCLOAK_HOSTNAME:-http://localhost:8081}/realms/${IDP_REALM:-tenant-a}"
# This path is read back by the ADS, Admin Service and Resource Service
# containers (issue #76), not by this seeder, so it has to be where the
# secret lands in *their* mount (docker-compose.yml's
# /run/secrets/idp-admin-credentials), not the go.sh sandbox's /workspace.
# tenantregistry validates the secret is readable at parse time, so this
# same path is also bind-mounted into the sandbox below, from the identical
# host file the app containers mount.
export TENANT_CREDENTIAL_SECRET_REF="${TENANT_CREDENTIAL_SECRET_REF:-/run/secrets/idp-admin-credentials}"
export GO_ENV_PASS="ASSIGNMENTSTORE_POSTGRES_DSN TENANT_REGISTRY_FILE TENANT_ISSUER TENANT_CREDENTIAL_SECRET_REF"
export GO_EXTRA_MOUNTS="${repo_root}/deploy/secrets/idp-admin-credentials:${TENANT_CREDENTIAL_SECRET_REF}:ro,z"

exec bash "${repo_root}/scripts/go.sh" libs/assignmentstore run ./cmd/seed-tenants
