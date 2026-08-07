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
COMPOSE="${COMPOSE:-docker compose --profile policy-release}"
GITEA_SERVICE="${GITEA_SERVICE:-gitea}"
GITEA_URL="${GITEA_URL:-http://localhost:${GITEA_PORT:-3001}}"
GITEA_ADMIN_USER="${GITEA_ADMIN_USER:-policy-release-admin}"
GITEA_ADMIN_PASSWORD="${GITEA_ADMIN_PASSWORD:-change-me}"
GITEA_ADMIN_EMAIL="${GITEA_ADMIN_EMAIL:-policy-release-admin@example.invalid}"
GITEA_ORG="${GITEA_ORG:-authz}"
GITEA_REPO_NAME="${GITEA_REPO_NAME:-root-policy}"
ROOT_POLICY_TAG="${ROOT_POLICY_TAG:-root-v1.0.0}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# post makes a POST request and treats 2xx and 409 (already exists) alike as
# success; anything else prints the response body and fails the script,
# rather than silently masking a real error as "already exists" the way a
# bare `|| echo` would.
post() {
  local url="$1" data="$2"
  local body status
  body="$(curl -s -u "${auth}" -X POST "${url}" -H "Content-Type: application/json" -d "${data}" -w '\n%{http_code}')"
  status="${body##*$'\n'}"
  body="${body%$'\n'*}"
  case "${status}" in
    2??) return 0 ;;
    # 409: repo already exists. 403: tag protection already exists
    # (routers/api/v1/repo/tag.go's CreateTagProtection returns Forbidden,
    # not Conflict, for that case). 422 with this message: org already
    # exists (orgs and users share Gitea's username namespace, confirmed
    # against a live Gitea 1.23).
    403|409) return 0 ;;
    422) [[ "${body}" == *"already exists"* ]] && return 0 || true ;;
  esac
  echo "    request to ${url} failed (HTTP ${status}): ${body}" >&2
  return 1
}

echo "==> Creating Gitea admin user (ignored if it already exists)"
if ! ${COMPOSE} exec -T -u git "${GITEA_SERVICE}" gitea admin user create \
  --admin \
  --username "${GITEA_ADMIN_USER}" \
  --password "${GITEA_ADMIN_PASSWORD}" \
  --email "${GITEA_ADMIN_EMAIL}" \
  --must-change-password=false 2>&1 | tee /tmp/gitea-seed-user-create.log; then
  grep -q "user already exists" /tmp/gitea-seed-user-create.log \
    || { echo "    admin user create failed, see above" >&2; exit 1; }
fi

auth="${GITEA_ADMIN_USER}:${GITEA_ADMIN_PASSWORD}"

echo "==> Creating organization ${GITEA_ORG} (ignored if it already exists)"
post "${GITEA_URL}/api/v1/orgs" "{\"username\":\"${GITEA_ORG}\"}"

echo "==> Creating repository ${GITEA_ORG}/${GITEA_REPO_NAME} (ignored if it already exists)"
post "${GITEA_URL}/api/v1/orgs/${GITEA_ORG}/repos" "{\"name\":\"${GITEA_REPO_NAME}\",\"auto_init\":false}"

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
# CreateTagProtection rejects a request with neither list populated ("both
# whitelist_usernames and whitelist_teams are empty", confirmed against a
# live Gitea 1.23): a tag protection with no allowlist would be
# unenforceable anyway, so the seeding admin is the allowlisted pusher.
post "${GITEA_URL}/api/v1/repos/${GITEA_ORG}/${GITEA_REPO_NAME}/tag_protections" \
  "{\"name_pattern\":\"root-v*\",\"whitelist_usernames\":[\"${GITEA_ADMIN_USER}\"]}"

echo "==> Creating an access token for the policy controller"
# GET .../tag_protections requires a token even for a public repo
# (confirmed against a live Gitea 1.23: it 401s with "token is required"
# under basic auth alone), so GiteaClient.ListTags cannot work
# unauthenticated. The token is written to .env (gitignored, never
# committed) rather than printed, since docker compose reads it from
# there for the policy-controller service's GITEA_TOKEN.
token_name="policy-controller-$(date +%s)"
token_response="$(curl -sf -u "${auth}" -X POST "${GITEA_URL}/api/v1/users/${GITEA_ADMIN_USER}/tokens" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"${token_name}\",\"scopes\":[\"read:repository\"]}")"
token="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["sha1"])' <<<"${token_response}")"

env_file="${repo_root}/.env"
touch "${env_file}"
grep -v '^POLICY_RELEASE_GITEA_TOKEN=' "${env_file}" > "${env_file}.tmp" || true
mv "${env_file}.tmp" "${env_file}"
echo "POLICY_RELEASE_GITEA_TOKEN=${token}" >> "${env_file}"

echo "==> Done. ${GITEA_ORG}/${GITEA_REPO_NAME}@${ROOT_POLICY_TAG} is ready for the policy controller to select."
echo "    Wrote POLICY_RELEASE_GITEA_TOKEN to ${env_file}; restart policy-controller to pick it up."
