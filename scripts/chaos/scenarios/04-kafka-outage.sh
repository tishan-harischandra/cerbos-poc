#!/usr/bin/env bash
# §18: "Kafka outage - Permission writes remain committed; revision
# reconciler catches up after recovery."
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"
# shellcheck source=scripts/tests/lib-token.sh
source "${repo_root}/scripts/tests/lib-token.sh"

admin_url="http://127.0.0.1:${ADMIN_CONSOLE_PORT}/api/admin/authz/tenants/tenant-a"
role="kc:cerbos-poc:patient-app:auditor"
result=0

token="$(token_for user-admin)"

echo "==> Reading the current permission revision"
# The previous scenario (03-database-outage) only just restored PostgreSQL.
# Unlike ads - whose pool is exercised continuously by 03's own
# decision_probe_loop and so has already cycled every connection that was
# stale against the pre-restart pod - admin-service takes no traffic during
# 03, so its pool's first opportunity to notice a pooled connection is dead
# and redial is this very read. That first read (and several more) can
# therefore return a genuine query error rather than merely being slow, so
# it is retried on any non-200 rather than failed outright, with a budget
# generous enough to cover pgxpool cycling through every stale connection.
revision_status=""
current_revision=""
for _ in $(seq 1 30); do
  revision_status="$(curl -sS -o /tmp/chaos-kafka-revision.json -w '%{http_code}' --max-time 5 \
    -H "Authorization: Bearer ${token}" "${admin_url}/permission-revision")"
  current_revision="$(jq -r '.revision' /tmp/chaos-kafka-revision.json 2>/dev/null || true)"
  if [[ "${revision_status}" == "200" ]] && [[ "${current_revision}" =~ ^[0-9]+$ ]]; then
    break
  fi
  sleep 2
done
if [[ "${revision_status}" != "200" ]] || ! [[ "${current_revision}" =~ ^[0-9]+$ ]]; then
  echo "FAIL a permission write commits while Kafka is paused (could not read the current permission revision: HTTP ${revision_status}: $(cat /tmp/chaos-kafka-revision.json 2>/dev/null))"
  exit 1
fi

echo "==> Pausing Kafka (scaling statefulset/redpanda to 0)"
kubectl_chaos scale statefulset/redpanda --replicas=0
kubectl_chaos wait --for=delete pod -l app=redpanda --timeout=60s

echo "==> Writing a role-permission change while Kafka is down"
write_body="$(jq -cn --argjson rev "${current_revision}" \
  '{expectedRevision: $rev, permissions: [{resourceKey: "patient_record", actionKey: "delete", enabled: true, validFrom: "2020-01-01T00:00:00Z"}]}')"
write_status="$(curl -sS -o /tmp/chaos-kafka-write.json -w '%{http_code}' --max-time 5 \
  -X PUT -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" \
  --data "${write_body}" "${admin_url}/roles/${role}/permissions")"
if [[ "${write_status}" != "200" ]]; then
  echo "FAIL a permission write commits while Kafka is paused (HTTP ${write_status}: $(cat /tmp/chaos-kafka-write.json))"
  result=1
else
  new_revision="$(jq -r '.revision' /tmp/chaos-kafka-write.json)"
  echo "ok   a permission write commits while Kafka is paused (revision ${current_revision} -> ${new_revision})"
fi

echo "==> Resuming Kafka"
kubectl_chaos scale statefulset/redpanda --replicas=1
wait_for_rollout statefulset/redpanda 120s

if [[ "${write_status}" == "200" ]]; then
  echo "==> Waiting for the revision reconciler to converge"
  converged=0
  for _ in $(seq 1 60); do
    body="$(curl -sS --max-time 5 -H "Authorization: Bearer ${token}" "${admin_url}/convergence")"
    if [[ "$(jq -r '.converged' <<<"${body}")" == "true" ]] \
      && [[ "$(jq -r '.actualRevision' <<<"${body}")" == "${new_revision}" ]]; then
      converged=1
      break
    fi
    sleep 1
  done
  if [[ "${converged}" -eq 1 ]]; then
    echo "ok   the revision reconciler converges on revision ${new_revision} after Kafka resumes"
  else
    echo "FAIL the revision reconciler converges after Kafka resumes (last: ${body:-none})"
    result=1
  fi
fi

exit "${result}"
