#!/usr/bin/env bash
# §18: "Partial root rollout - Release is not marked active; failing pod is
# removed from service or remediated." (acceptance: "A failed policy rollout
# leaves the previous archive active and the release not marked active.")
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"
# shellcheck source=scripts/tests/lib-token.sh
source "${repo_root}/scripts/tests/lib-token.sh"

GITEA_URL="http://127.0.0.1:${CHAOS_GITEA_PORT}"
GITEA_ADMIN_USER="${GITEA_ADMIN_USER:-policy-release-admin}"
GITEA_ADMIN_PASSWORD="${GITEA_ADMIN_PASSWORD:-change-me}"
GITEA_ORG="${GITEA_ORG:-authz}"
GITEA_REPO_NAME="${GITEA_REPO_NAME:-root-policy}"
BROKEN_TAG="root-v9.9.9-broken"
result=0

token="$(token_for user-admin)"
releases_url="http://127.0.0.1:${ADMIN_CONSOLE_PORT}/api/admin/authz/policy-releases"

echo "==> Reading the currently active release"
before_current="$(curl -sS --max-time 5 -H "Authorization: Bearer ${token}" "${releases_url}" | jq -r '.current.revision')"
if [[ -z "${before_current}" || "${before_current}" == "null" ]]; then
  echo "FAIL a failed policy rollout leaves the previous archive active (no release was active before the test)"
  exit 1
fi

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

echo "==> Pushing an invalid policy tree at ${BROKEN_TAG}"
git clone -q "http://${GITEA_ADMIN_USER}:${GITEA_ADMIN_PASSWORD}@127.0.0.1:${CHAOS_GITEA_PORT}/${GITEA_ORG}/${GITEA_REPO_NAME}.git" "${work_dir}"
# Deliberately unparsable YAML: the validation gate (§13.2) runs `cerbos
# compile` against the fetched commit before any archive is installed, so
# this must never reach the store cerbos-managed serves from.
echo "this is: not: valid: cerbos: policy: [" > "${work_dir}/policies/patient_record.yaml"
git -C "${work_dir}" add -A
git -C "${work_dir}" -c user.email="${GITEA_ADMIN_USER}@example.invalid" -c user.name="${GITEA_ADMIN_USER}" \
  commit -q -m "Introduce an invalid policy on purpose"
git -C "${work_dir}" tag -f "${BROKEN_TAG}"
git -C "${work_dir}" push -q origin HEAD:refs/heads/main
git -C "${work_dir}" push -q origin "refs/tags/${BROKEN_TAG}"
curl -sS -u "${GITEA_ADMIN_USER}:${GITEA_ADMIN_PASSWORD}" -X POST \
  "${GITEA_URL}/api/v1/repos/${GITEA_ORG}/${GITEA_REPO_NAME}/tag_protections" \
  -H 'Content-Type: application/json' \
  -d "{\"name_pattern\":\"${BROKEN_TAG}\",\"whitelist_usernames\":[\"${GITEA_ADMIN_USER}\"]}" >/dev/null || true

echo "==> Waiting for the controller to see and reject the broken tag"
rejected=0
for _ in $(seq 1 60); do
  history="$(curl -sS --max-time 5 -H "Authorization: Bearer ${token}" "${releases_url}")"
  if jq -e --arg tag "${BROKEN_TAG}" \
    '.history[] | select(.revision == $tag and .activated == false)' <<<"${history}" >/dev/null; then
    rejected=1
    break
  fi
  sleep 2
done

if [[ "${rejected}" -eq 1 ]]; then
  echo "ok   the failed release is recorded and not marked active"
else
  echo "FAIL the failed release is recorded and not marked active (never observed in history: ${history:-none})"
  result=1
fi

after_current="$(curl -sS --max-time 5 -H "Authorization: Bearer ${token}" "${releases_url}" | jq -r '.current.revision')"
if [[ "${after_current}" == "${before_current}" ]]; then
  echo "ok   the previous archive stays active (${before_current})"
else
  echo "FAIL the previous archive stays active (was ${before_current}, now ${after_current})"
  result=1
fi

exit "${result}"
