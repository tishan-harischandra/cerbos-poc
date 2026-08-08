#!/usr/bin/env bash
# Seeds the dev-chaos overlay's Gitea with the root policy repository, the
# same way scripts/gitea-seed.sh does for docker compose's policy-release
# profile (issue #21, §13). Requires start_port_forwards to already have
# svc/gitea reachable at 127.0.0.1:${CHAOS_GITEA_PORT}. Safe to re-run.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"

GITEA_URL="http://127.0.0.1:${CHAOS_GITEA_PORT}"
GITEA_ADMIN_USER="${GITEA_ADMIN_USER:-policy-release-admin}"
GITEA_ADMIN_PASSWORD="${GITEA_ADMIN_PASSWORD:-change-me}"
GITEA_ADMIN_EMAIL="${GITEA_ADMIN_EMAIL:-policy-release-admin@example.invalid}"
GITEA_ORG="${GITEA_ORG:-authz}"
GITEA_REPO_NAME="${GITEA_REPO_NAME:-root-policy}"
ROOT_POLICY_TAG="${ROOT_POLICY_TAG:-root-v1.0.0}"

post() {
  local url="$1" data="$2"
  local body status
  body="$(curl -s -u "${auth}" -X POST "${url}" -H "Content-Type: application/json" -d "${data}" -w '\n%{http_code}')"
  status="${body##*$'\n'}"
  body="${body%$'\n'*}"
  case "${status}" in
    2??) return 0 ;;
    403|409) return 0 ;;
    422) [[ "${body}" == *"already exists"* ]] && return 0 || true ;;
  esac
  echo "    request to ${url} failed (HTTP ${status}): ${body}" >&2
  return 1
}

echo "==> Waiting for Gitea"
for _ in $(seq 1 60); do
  curl -sS --max-time 5 -o /dev/null "${GITEA_URL}/api/healthz" && break
  sleep 1
done

echo "==> Creating Gitea admin user (ignored if it already exists)"
# kubectl exec has no docker/podman-style `-u git` flag, and Gitea refuses
# to run as the root user `exec` defaults to (see docker-compose.yml's
# gitea-seed.sh, which uses `-u git` there); `su git -c` is the k8s-side
# equivalent since s6 already runs the real gitea process as git (confirmed
# against a live pod, issue #26).
if ! kubectl_chaos exec deploy/gitea -- su git -c "gitea admin user create \
  --admin \
  --username '${GITEA_ADMIN_USER}' \
  --password '${GITEA_ADMIN_PASSWORD}' \
  --email '${GITEA_ADMIN_EMAIL}' \
  --must-change-password=false" 2>&1 | tee /tmp/chaos-gitea-seed-user-create.log; then
  grep -q "user already exists" /tmp/chaos-gitea-seed-user-create.log \
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
post "${GITEA_URL}/api/v1/repos/${GITEA_ORG}/${GITEA_REPO_NAME}/tag_protections" \
  "{\"name_pattern\":\"root-v*\",\"whitelist_usernames\":[\"${GITEA_ADMIN_USER}\"]}"

echo "==> Creating an access token for the policy controller"
token_name="policy-controller-$(date +%s)"
token_response="$(curl -sf -u "${auth}" -X POST "${GITEA_URL}/api/v1/users/${GITEA_ADMIN_USER}/tokens" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"${token_name}\",\"scopes\":[\"read:repository\"]}")"
token="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["sha1"])' <<<"${token_response}")"

echo "==> Rolling the token into the policy-controller secret"
kubectl_chaos create secret generic policy-controller-gitea-token \
  --from-literal=GITEA_TOKEN="${token}" \
  --dry-run=client -o yaml | kubectl_chaos apply -f -
kubectl_chaos rollout restart deployment/policy-controller
wait_for_rollout deployment/policy-controller 180s

echo "==> Done. ${GITEA_ORG}/${GITEA_REPO_NAME}@${ROOT_POLICY_TAG} is ready for the policy controller to select."
