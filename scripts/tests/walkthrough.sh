#!/usr/bin/env bash
# The README's guided walkthrough, executed rather than described.
#
# A walkthrough written only in prose rots the first time an endpoint moves,
# and nobody notices until a newcomer follows it on a clean machine. This is
# the same sequence the README asks a human to click through, driven over the
# same HTTP surface the Admin Console and the Business UI use:
#
#   1. log in as an administrator and as a clinician, against the real IdP
#   2. read the role matrix for the clinician's role
#   3. ask what composite UI capabilities one resource-action affects
#      (the Admin Console's impact preview)
#   4. grant that permission with the expected-revision precondition
#   5. watch the Business UI's capability snapshot flip, and time it
#   6. confirm no policy release happened anywhere in that path
#   7. put the matrix back, and watch it flip back
#
# It runs against any deployment that publishes the Administration Service and
# Keycloak - `make up` by default, or a kind/minikube cluster with two
# port-forwards:
#
#   WALKTHROUGH_BASE=http://127.0.0.1:8081 KEYCLOAK_URL=http://127.0.0.1:8080 \
#     bash scripts/tests/walkthrough.sh
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
# shellcheck source=scripts/tests/lib-token.sh
source scripts/tests/lib-token.sh

BASE="${WALKTHROUGH_BASE:-http://127.0.0.1:${ADMIN_CONSOLE_PORT:-4200}}"
ADMIN_URL="${BASE}/api/admin"
ADS_URL="${BASE}/api/ads"

# The demo installation, as seeded by libs/assignmentstore/demoseed.
TENANT="${WALKTHROUGH_TENANT:-tenant-a}"
ROLE="${WALKTHROUGH_ROLE:-kc:tenant-a:patient-app:doctor}"
# The permission the walkthrough grants. The demo matrix deliberately leaves
# it off, so patient.route.details - which needs it - starts denied and the
# grant is visible rather than a no-op.
GRANT_RESOURCE="${WALKTHROUGH_RESOURCE:-person}"
GRANT_ACTION="${WALKTHROUGH_ACTION:-read}"
# The capability the Business UI guards its patient detail route with.
CAPABILITY="${WALKTHROUGH_CAPABILITY:-patient.route.details}"
MODULE="clinical"
PATIENT="${WALKTHROUGH_PATIENT:-patient-456}"
# §15.3's convergence objective. A walkthrough that says "within seconds" and
# waits a minute is not the walkthrough anyone read.
CONVERGENCE_BUDGET_SECONDS="${WALKTHROUGH_CONVERGENCE_BUDGET:-15}"

failures=0
pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }

save_body_file="$(mktemp -t walkthrough-save.XXXXXX)"
trap 'rm -f "${save_body_file}"' EXIT

command -v jq >/dev/null 2>&1 || { echo "walkthrough: jq is required" >&2; exit 1; }

# url-encodes one path segment. Canonical role identifiers carry colons, and a
# raw colon in a path segment is a different URL than the one the matrix is
# keyed by.
encode_segment() {
  jq -rn --arg value "$1" '$value | @uri'
}

# capability_allowed <token> - echoes true/false for CAPABILITY, as the
# Business UI's instance-context snapshot would receive it.
capability_allowed() {
  local token="$1" body
  body="$(curl -sS --max-time 10 \
    -H "Authorization: Bearer ${token}" \
    -H 'Content-Type: application/json' \
    --data "$(jq -nc \
      --arg module "${MODULE}" \
      --arg capability "${CAPABILITY}" \
      --arg patient "${PATIENT}" \
      '{module: $module, capabilityKeys: [$capability], context: {patientId: $patient}}')" \
    "${ADS_URL}/internal/capabilities/evaluate")" || return 1
  jq -r --arg capability "${CAPABILITY}" '.capabilities[$capability].allowed // false' <<<"${body}"
}

# wait_for_capability <token> <expected> - polls until the snapshot reports
# <expected>, echoing the elapsed whole seconds. Returns 1 on timeout.
wait_for_capability() {
  local token="$1" expected="$2" started elapsed
  started="$(date +%s)"
  while :; do
    if [[ "$(capability_allowed "${token}")" == "${expected}" ]]; then
      echo $(( $(date +%s) - started ))
      return 0
    fi
    elapsed=$(( $(date +%s) - started ))
    if (( elapsed >= CONVERGENCE_BUDGET_SECONDS )); then
      echo "${elapsed}"
      return 1
    fi
    sleep 1
  done
}

# save_matrix <admin-token> <enabled> - replays every row the role already
# carries plus the walkthrough's row at <enabled>, exactly as the console's
# save does, under the revision it just read.
save_matrix() {
  local token="$1" enabled="$2" role_path current revision body status
  role_path="$(encode_segment "${ROLE}")"

  current="$(curl -sS --max-time 10 -H "Authorization: Bearer ${token}" \
    "${ADMIN_URL}/authz/tenants/${TENANT}/roles/${role_path}/permissions")" || return 1
  revision="$(jq -r '.revision' <<<"${current}")"

  body="$(jq -c \
    --argjson revision "${revision}" \
    --arg resource "${GRANT_RESOURCE}" \
    --arg action "${GRANT_ACTION}" \
    --argjson enabled "${enabled}" \
    '{
       expectedRevision: $revision,
       permissions: (
         [.permissions[] | select(.resourceKey != $resource or .actionKey != $action)]
         + [{resourceKey: $resource, actionKey: $action, enabled: $enabled,
             validFrom: "2020-01-01T00:00:00Z"}]
       )
     }' <<<"${current}")"

  status="$(curl -sS --max-time 10 -o "${save_body_file}" -w '%{http_code}' \
    -X PUT -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
    --data "${body}" \
    "${ADMIN_URL}/authz/tenants/${TENANT}/roles/${role_path}/permissions")"
  if [[ "${status}" != "200" ]]; then
    echo "walkthrough: the save was refused (HTTP ${status}): $(cat "${save_body_file}")" >&2
    return 1
  fi
  jq -r '.revision' "${save_body_file}"
}

# root_policy_revision <admin-token> - the immutable policy release the PDP is
# serving. It must be the same string at the end as at the beginning.
root_policy_revision() {
  curl -sS --max-time 10 -H "Authorization: Bearer $1" \
    "${ADMIN_URL}/authz/resources" | jq -r '.rootPolicyRevision // empty'
}

echo "--- 1. log in ---"

wait_for_keycloak || exit 1
admin_token="$(token_for user-admin)" || exit 1
pass "an administrator logs in against the real identity provider"
doctor_token="$(token_for user-doctor)" || exit 1
pass "a clinician logs in against the real identity provider"

echo
echo "--- 2. what the Business UI can do today ---"

before="$(capability_allowed "${doctor_token}")"
if [[ "${before}" == "false" ]]; then
  pass "${CAPABILITY} starts denied, so the grant below is visible"
else
  fail "${CAPABILITY} starts ${before}, want false - the demo matrix is not in its seeded state"
fi

policy_before="$(root_policy_revision "${admin_token}")"
if [[ -n "${policy_before}" ]]; then
  pass "the PDP is serving root policy revision ${policy_before}"
else
  fail "the administration API did not report a root policy revision"
fi

echo
echo "--- 3. the impact preview ---"

impact="$(curl -sS --max-time 10 -H "Authorization: Bearer ${admin_token}" \
  "${ADMIN_URL}/authz/resources/${GRANT_RESOURCE}/actions/${GRANT_ACTION}/capabilities")"
if jq -e --arg capability "${CAPABILITY}" \
  '.capabilities | map(.key) | index($capability)' >/dev/null <<<"${impact}"; then
  pass "the impact preview warns that ${GRANT_RESOURCE}:${GRANT_ACTION} affects ${CAPABILITY}"
else
  fail "the impact preview did not name ${CAPABILITY}: ${impact}"
fi

echo
echo "--- 4. grant the permission ---"

revision="$(save_matrix "${admin_token}" true)" || exit 1
pass "the role matrix saved at revision ${revision}"

echo
echo "--- 5. the Business UI follows, with no reload of anything ---"

if elapsed="$(wait_for_capability "${doctor_token}" true)"; then
  pass "${CAPABILITY} became allowed ${elapsed}s after the save"
else
  fail "${CAPABILITY} was still denied ${elapsed}s after the save"
fi

echo
echo "--- 6. nothing was rebuilt or released ---"

policy_after="$(root_policy_revision "${admin_token}")"
if [[ "${policy_after}" == "${policy_before}" ]]; then
  pass "the root policy revision is unchanged (${policy_after}): no policy rebuild in this path"
else
  fail "the root policy revision moved from ${policy_before} to ${policy_after}"
fi

echo
echo "--- 7. put it back ---"

revision="$(save_matrix "${admin_token}" false)" || exit 1
pass "the grant was withdrawn at revision ${revision}"

if elapsed="$(wait_for_capability "${doctor_token}" false)"; then
  pass "${CAPABILITY} became denied again ${elapsed}s after the save"
else
  fail "${CAPABILITY} was still allowed ${elapsed}s after the withdrawal"
fi

if (( failures > 0 )); then
  echo
  echo "${failures} walkthrough failure(s)"
  exit 1
fi

echo
echo "the guided walkthrough works exactly as written"
