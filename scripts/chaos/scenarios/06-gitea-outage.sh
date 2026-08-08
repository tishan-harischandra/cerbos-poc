#!/usr/bin/env bash
# §18: "Gitea outage - Existing root policy archive remains active."
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"
# shellcheck source=scripts/tests/lib-token.sh
source "${repo_root}/scripts/tests/lib-token.sh"

token="$(token_for user-admin)"
releases_url="http://127.0.0.1:${ADMIN_CONSOLE_PORT}/api/admin/authz/policy-releases"
result=0

before_current="$(curl -sS --max-time 5 -H "Authorization: Bearer ${token}" "${releases_url}" | jq -r '.current.revision')"

echo "==> Stopping Gitea"
kubectl_chaos scale deployment/gitea --replicas=0
kubectl_chaos wait --for=delete pod -l app=gitea --timeout=60s

sleep 5
after_current="$(curl -sS --max-time 5 -H "Authorization: Bearer ${token}" "${releases_url}" | jq -r '.current.revision')"
if [[ "${after_current}" == "${before_current}" ]]; then
  echo "ok   the active root revision keeps serving while Gitea is stopped (${after_current})"
else
  echo "FAIL the active root revision keeps serving while Gitea is stopped (was ${before_current}, now ${after_current})"
  result=1
fi

echo "==> Restoring Gitea"
kubectl_chaos scale deployment/gitea --replicas=1
wait_for_rollout deployment/gitea 180s

exit "${result}"
