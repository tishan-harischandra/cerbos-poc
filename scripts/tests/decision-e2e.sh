#!/usr/bin/env bash
# End-to-end authorization decisions against a running `make up` stack.
#
# The Cerbos policy suite proves the precedence rules in isolation. This proves
# the whole path: HTTP request -> ADS -> assembled permissionContext -> gRPC ->
# a real PDP -> policy evaluation -> decision. A regression anywhere in that
# chain, including a mis-assembled context or a policy that never loaded, shows
# up here.
set -uo pipefail

PORT="${ADMIN_CONSOLE_PORT:-4200}"
CHECK_URL="http://127.0.0.1:${PORT}/api/ads/internal/authz/check"
failures=0

command -v jq >/dev/null 2>&1 || { echo "decision-e2e: jq is required" >&2; exit 1; }

# The ADS container is live as soon as /healthz answers, but a decision also needs
# the PDP, so wait for /readyz to agree before asserting on behaviour.
#
# Several consecutive readings are required rather than one. A PDP that is going
# down still answers for a moment, and a single ok taken during that window would
# let the suite start asserting into a restart and report behavioural failures for
# what is really a timing artefact.
READY_URL="http://127.0.0.1:${PORT}/api/ads/readyz"
REQUIRED_STREAK=3
streak=0

for _ in $(seq 1 40); do
  if curl -fsS --max-time 3 "${READY_URL}" 2>/dev/null | grep -q '"cerbos":"ok"'; then
    streak=$((streak + 1))
    if (( streak >= REQUIRED_STREAK )); then
      break
    fi
  else
    streak=0
  fi
  sleep 1
done

if (( streak < REQUIRED_STREAK )); then
  echo "decision-e2e: the ADS never held the PDP reachable at ${READY_URL}" >&2
  curl -sS --max-time 3 "${READY_URL}" >&2 || true
  exit 1
fi

# decide <principal> <status> <resource-tenant> <actions-json> [extra-idp-role]
# Echoes the decision response body.
decide() {
  local principal="$1" status="$2" resource_tenant="$3" actions="$4" extra_role="${5:-}"
  local roles='["kc:realm:patient-app:doctor"]'
  if [[ -n "${extra_role}" ]]; then
    roles="$(jq -cn --arg extra "${extra_role}" '["kc:realm:patient-app:doctor", $extra]')"
  fi

  jq -cn \
    --arg principal "${principal}" \
    --arg status "${status}" \
    --arg resourceTenant "${resource_tenant}" \
    --argjson actions "${actions}" \
    --argjson idpRoles "${roles}" \
    '{
      tenantId: "tenant-a",
      hospitalId: "hospital-1",
      principalId: $principal,
      idpRoles: $idpRoles,
      resources: [{
        kind: "patient_record",
        id: "patient-456",
        attributes: {tenantId: $resourceTenant, hospitalId: "hospital-1", status: $status},
        actions: $actions
      }]
    }' \
  | curl -sS -o /tmp/decision-body -w '%{http_code}' \
      -H 'Content-Type: application/json' \
      -H 'X-Correlation-Id: decision-e2e' \
      --data @- "${CHECK_URL}"
}

# expect_decision <description> <principal> <status> <tenant> <action> <allowed>
expect_decision() {
  local description="$1" principal="$2" status="$3" tenant="$4" action="$5" expected="$6"

  local code
  code="$(decide "${principal}" "${status}" "${tenant}" "$(jq -cn --arg a "${action}" '[$a]')")"
  if [[ "${code}" != "200" ]]; then
    echo "FAIL ${description} (HTTP ${code}: $(cat /tmp/decision-body))"
    failures=$((failures + 1))
    return
  fi

  local actual
  actual="$(jq -r --arg a "${action}" '.resources[0].actions[$a].allowed' /tmp/decision-body)"
  if [[ "${actual}" == "${expected}" ]]; then
    echo "ok   ${description}"
  else
    echo "FAIL ${description} (${action} allowed=${actual}, want ${expected})"
    failures=$((failures + 1))
  fi
}

echo "--- the seven-case matrix, end to end through a real PDP ---"

# A role grant allows.
expect_decision "a role grant allows read" \
  user-doctor ACTIVE tenant-a read true
expect_decision "a role grant allows update" \
  user-doctor ACTIVE tenant-a update true

# The case ADR-003 exists for: a user revoke beating a role grant, all the way
# through the stack rather than only inside a policy test.
expect_decision "a user revoke denies update even though a role grants it" \
  user-doctor-revoked ACTIVE tenant-a update false
expect_decision "the same principal still reads, so the revoke was action-scoped" \
  user-doctor-revoked ACTIVE tenant-a read true

# A user grant with no role grant behind it.
expect_decision "a user grant allows read with no role grant" \
  user-clerk-granted ACTIVE tenant-a read true
expect_decision "an action nobody granted is denied" \
  user-clerk-granted ACTIVE tenant-a delete false

# Default deny.
expect_decision "a principal with no assignments is denied" \
  user-unassigned ACTIVE tenant-a read false

# Mandatory restrictions outrank every grant.
expect_decision "a locked record denies update despite a role grant" \
  user-doctor LOCKED tenant-a update false
expect_decision "a locked record still allows read" \
  user-doctor LOCKED tenant-a read true

# Tenant isolation is mandatory too.
expect_decision "a record in another tenant is denied" \
  user-doctor ACTIVE tenant-b read false

echo
echo "--- the decision contract ---"

code="$(decide user-doctor ACTIVE tenant-a '["read","update"]')"
[[ "${code}" == "200" ]]
check_code=$?
if [[ ${check_code} -eq 0 ]]; then echo "ok   a batched request answers for every action"; else
  echo "FAIL a batched request answers for every action (HTTP ${code})"; failures=$((failures + 1)); fi

answered="$(jq -r '.resources[0].actions | keys | join(",")' /tmp/decision-body)"
if [[ "${answered}" == "read,update" ]]; then
  echo "ok   both requested actions are answered"
else
  echo "FAIL both requested actions are answered (got ${answered})"
  failures=$((failures + 1))
fi

call_id="$(jq -r '.cerbosCallId' /tmp/decision-body)"
if [[ -n "${call_id}" && "${call_id}" != "null" ]]; then
  echo "ok   the response carries the cerbosCallId for audit correlation"
else
  echo "FAIL the response carries the cerbosCallId for audit correlation"
  failures=$((failures + 1))
fi

revision="$(jq -r '.resources[0].permissionRevision' /tmp/decision-body)"
if [[ "${revision}" == "184" ]]; then
  echo "ok   the response reports the permission revision it decided at"
else
  echo "FAIL the response reports the permission revision (got ${revision})"
  failures=$((failures + 1))
fi

# The synthetic role is the platform's to inject. A token claiming it must be
# refused before a decision is attempted.
code="$(decide user-doctor ACTIVE tenant-a '["read"]' "sys:permission-evaluator")"
if [[ "${code}" == "403" ]]; then
  echo "ok   a token presenting the synthetic role is refused"
else
  echo "FAIL a token presenting the synthetic role is refused (HTTP ${code})"
  failures=$((failures + 1))
fi

# Schema enforcement at the PDP: a request whose resource carries no
# permissionContext must never reach a rule. The ADS always assembles one, so
# this is asserted directly against Cerbos in the policy suite; here we check the
# ADS refuses a structurally invalid request instead of forwarding it.
code="$(curl -sS -o /tmp/decision-body -w '%{http_code}' \
  -H 'Content-Type: application/json' \
  --data '{"tenantId":"tenant-a","hospitalId":"hospital-1","principalId":"user-doctor","resources":[]}' \
  "${CHECK_URL}")"
if [[ "${code}" == "400" ]]; then
  echo "ok   a request with no resources is rejected"
else
  echo "FAIL a request with no resources is rejected (HTTP ${code})"
  failures=$((failures + 1))
fi

rm -f /tmp/decision-body

if (( failures > 0 )); then
  echo
  echo "${failures} decision failure(s)"
  exit 1
fi

echo
echo "end-to-end decisions passed"
