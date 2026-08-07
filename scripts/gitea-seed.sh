#!/usr/bin/env bash
# Seeds the policy-release profile's Gitea instance with the root policy
# repository (issue #21, §13): creates an admin user, creates the repo,
# pushes the committed catalog+policies tree, tags it and protects the tag
# so the controller's SelectTag never selects an unprotected one.
#
# Run once after `docker compose --profile policy-release up -d gitea`:
#   scripts/gitea-seed.sh
#
# Safe to re-run: every step is idempotent (existing user/repo/tag is left
# alone rather than failing the whole script).
set -euo pipefail

DOCKER="${DOCKER:-docker}"
COMPOSE="${COMPOSE:-docker compose}"
GITEA_SERVICE="${GITEA_SERVICE:-gitea}"
GITEA_URL="${GITEA_URL:-http://localhost:3000}"
GITEA_ADMIN_USER="${GITEA_ADMIN_USER:-policy-release-admin}"
GITEA_ADMIN_PASSWORD="${GITEA_ADMIN_PASSWORD:-change-me}"
GITEA_ADMIN_EMAIL="${GITEA_ADMIN_EMAIL:-policy-release-admin@example.invalid}"
GITEA_ORG="${GITEA_ORG:-authz}"
GITEA_REPO_NAME="${GITEA_REPO_NAME:-root-policy}"
ROOT_POLICY_TAG="${ROOT_POLICY_TAG:-root-v1.0.0}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "==> Creating Gitea admin user (ignored if it already exists)"
${COMPOSE} exec -T "${GITEA_SERVICE}" gitea admin user create \
  --admin \
  --username "${GITEA_ADMIN_USER}" \
  --password "${GITEA_ADMIN_PASSWORD}" \
  --email "${GITEA_ADMIN_EMAIL}" \
  --must-change-password=false \
  || echo "    (admin user already exists)"

auth="${GITEA_ADMIN_USER}:${GITEA_ADMIN_PASSWORD}"

echo "==> Creating organization ${GITEA_ORG} (ignored if it already exists)"
curl -sf -u "${auth}" -X POST "${GITEA_URL}/api/v1/orgs" \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"${GITEA_ORG}\"}" >/dev/null \
  || echo "    (organization already exists)"

echo "==> Creating repository ${GITEA_ORG}/${GITEA_REPO_NAME} (ignored if it already exists)"
curl -sf -u "${auth}" -X POST "${GITEA_URL}/api/v1/orgs/${GITEA_ORG}/repos" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"${GITEA_REPO_NAME}\",\"auto_init\":false}" >/dev/null \
  || echo "    (repository already exists)"

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

echo "==> Assembling the root policy tree"
mkdir -p "${work_dir}/catalog" "${work_dir}/policies"
cp -r "${repo_root}/deploy/cerbos/catalog/." "${work_dir}/catalog/"
cp -r "${repo_root}/deploy/cerbos/policies/." "${work_dir}/policies/"

git -C "${work_dir}" init -q
git -C "${work_dir}" config user.email "${GITEA_ADMIN_EMAIL}"
git -C "${work_dir}" config user.name "${GITEA_ADMIN_USER}"
git -C "${work_dir}" add -A
git -C "${work_dir}" commit -q -m "Seed root policy tree from deploy/cerbos" --allow-empty

remote_url="${GITEA_URL/http:\/\//http://${GITEA_ADMIN_USER}:${GITEA_ADMIN_PASSWORD}@}/${GITEA_ORG}/${GITEA_REPO_NAME}.git"

echo "==> Pushing to Gitea"
git -C "${work_dir}" push -q -f "${remote_url}" HEAD:refs/heads/main

echo "==> Tagging ${ROOT_POLICY_TAG}"
git -C "${work_dir}" tag -f "${ROOT_POLICY_TAG}"
git -C "${work_dir}" push -q -f "${remote_url}" "refs/tags/${ROOT_POLICY_TAG}"

echo "==> Protecting tag pattern root-v*"
curl -sf -u "${auth}" -X POST "${GITEA_URL}/api/v1/repos/${GITEA_ORG}/${GITEA_REPO_NAME}/tags/protection" \
  -H "Content-Type: application/json" \
  -d '{"name_pattern":"root-v*"}' >/dev/null \
  || echo "    (tag protection already exists)"

echo "==> Done. ${GITEA_ORG}/${GITEA_REPO_NAME}@${ROOT_POLICY_TAG} is ready for the policy controller to select."
