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

wait_for_decisions() {
  # Gate on the decision path itself rather than on /readyz. Readiness probes the
  # PDP over its own connection, so it reports healthy while the decision
  # channel is still reconnecting, and it keeps saying healthy for a moment after
  # a PDP that is going down stops being able to answer. Asking the real endpoint
  # is the only signal that means what this suite needs.
  #
  # Consecutive successes, because one success during a restart proves nothing.
  local required=3 streak=0 code
  local probe='{"tenantId":"tenant-a","hospitalId":"hospital-1","principalId":"user-unassigned",
    "idpRoles":[],"resources":[{"kind":"patient_record","id":"patient-000",
    "attributes":{"tenantId":"tenant-a","hospitalId":"hospital-1","status":"ACTIVE"},
    "actions":["read"]}]}'

  for _ in $(seq 1 60); do
    code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
      -H 'Content-Type: application/json' --data "${probe}" "${CHECK_URL}" 2>/dev/null)"
    if [[ "${code}" == "200" ]]; then
      streak=$((streak + 1))
      if (( streak >= required )); then
        return 0
      fi
    else
      streak=0
    fi
    sleep 1
  done

  echo "decision-e2e: the ADS never served ${required} decisions in a row; last HTTP ${code}" >&2
  curl -sS --max-time 5 "http://127.0.0.1:${PORT}/api/ads/readyz" >&2 || true
  return 1
}

wait_for_decisions || exit 1

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
batched_body="$(cat /tmp/decision-body)"

if [[ "${code}" == "200" ]]; then
  echo "ok   a batched request answers for every action"
else
  echo "FAIL a batched request answers for every action (HTTP ${code}: ${batched_body})"
  failures=$((failures + 1))
fi

# Every assertion below reads the same response. Reporting the body when one
# fails is what makes a flake diagnosable rather than a mystery.
contract_check() {
  local description="$1" expression="$2" expected="$3"
  local actual
  actual="$(jq -r "${expression}" <<<"${batched_body}" 2>&1)"
  if [[ "${actual}" == "${expected}" ]]; then
    echo "ok   ${description}"
  else
    echo "FAIL ${description} (${expression} = '${actual}', want '${expected}')"
    echo "     response was: ${batched_body}"
    failures=$((failures + 1))
  fi
}

contract_check "both requested actions are answered" \
  '.resources[0].actions | keys | join(",")' "read,update"
contract_check "the response reports the permission revision it decided at" \
  '.resources[0].permissionRevision' "184"
contract_check "the response names the resource it decided about" \
  '.resources[0].kind' "patient_record"

call_id="$(jq -r '.cerbosCallId' <<<"${batched_body}" 2>&1)"
if [[ -n "${call_id}" && "${call_id}" != "null" ]]; then
  echo "ok   the response carries the cerbosCallId for audit correlation"
else
  echo "FAIL the response carries the cerbosCallId for audit correlation"
  echo "     response was: ${batched_body}"
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
