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

# Two realms (issue #77): each names its own issuer and credential secret
# path, substituted independently by cmd/seed-tenants. These paths are read
# back by the ADS, Admin Service and Resource Service containers, not by
# this seeder, so they have to be where the secret lands in *their* mount
# (docker-compose.yml's /run/secrets/idp-admin-credentials[-tenant-b]), not
# the go.sh sandbox's /workspace. tenantregistry validates each secret is
# readable at parse time, so the same paths are also bind-mounted into the
# sandbox below, from the identical host files the app containers mount.
export TENANT_A_ISSUER="${TENANT_A_ISSUER:-${KEYCLOAK_HOSTNAME:-http://localhost:8081}/realms/tenant-a}"
export TENANT_A_CREDENTIAL_SECRET_REF="${TENANT_A_CREDENTIAL_SECRET_REF:-/run/secrets/idp-admin-credentials}"
export TENANT_B_ISSUER="${TENANT_B_ISSUER:-${KEYCLOAK_HOSTNAME:-http://localhost:8081}/realms/tenant-b}"
export TENANT_B_CREDENTIAL_SECRET_REF="${TENANT_B_CREDENTIAL_SECRET_REF:-/run/secrets/idp-admin-credentials-tenant-b}"
export GO_ENV_PASS="ASSIGNMENTSTORE_POSTGRES_DSN TENANT_REGISTRY_FILE TENANT_A_ISSUER TENANT_A_CREDENTIAL_SECRET_REF TENANT_B_ISSUER TENANT_B_CREDENTIAL_SECRET_REF"
export GO_EXTRA_MOUNTS="$(printf '%s\n%s' \
  "${repo_root}/deploy/secrets/idp-admin-credentials:${TENANT_A_CREDENTIAL_SECRET_REF}:ro,z" \
  "${repo_root}/deploy/secrets/idp-admin-credentials-tenant-b:${TENANT_B_CREDENTIAL_SECRET_REF}:ro,z")"

exec bash "${repo_root}/scripts/go.sh" libs/assignmentstore run ./cmd/seed-tenants
