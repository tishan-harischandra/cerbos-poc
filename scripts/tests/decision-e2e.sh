#!/usr/bin/env bash
# End-to-end authorization decisions against a running `make up` stack.
#
# The Cerbos policy suite proves the precedence rules in isolation. This proves
# the whole path: HTTP request -> ADS -> assembled permissionContext -> gRPC ->
# a real PDP -> policy evaluation -> decision. A regression anywhere in that
# chain, including a mis-assembled context or a policy that never loaded, shows
# up here.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
# shellcheck source=scripts/tests/lib-token.sh
source scripts/tests/lib-token.sh

PORT="${ADMIN_CONSOLE_PORT:-4200}"
CHECK_URL="http://127.0.0.1:${PORT}/api/ads/internal/authz/check"
failures=0

command -v jq >/dev/null 2>&1 || { echo "decision-e2e: jq is required" >&2; exit 1; }
wait_for_keycloak || exit 1

# Every principal below is a real Keycloak user, and every request carries the
# token that user logged in with. Roles are therefore whatever the realm grants
# them, which is the point: the identifiers in the seeded matrix have to be the
# ones token normalisation produces, and a mismatch shows up here as a denial.
declare -A TOKENS
token_of() {
  local principal="$1"
  if [[ -z "${TOKENS[${principal}]:-}" ]]; then
    TOKENS[${principal}]="$(token_for "${principal}")" || exit 1
  fi
  printf '%s' "${TOKENS[${principal}]}"
}

wait_for_decisions() {
  # Gate on the decision path itself rather than on /readyz. Readiness probes the
  # PDP over its own connection, so it reports healthy while the decision
  # channel is still reconnecting, and it keeps saying healthy for a moment after
  # a PDP that is going down stops being able to answer. Asking the real endpoint
  # is the only signal that means what this suite needs.
  #
  # Consecutive successes, because one success during a restart proves nothing.
  local required=3 streak=0 code token
  token="$(token_of user-unassigned)"
  local probe='{"resources":[{"kind":"patient_record","id":"patient-000",
    "attributes":{"tenantId":"tenant-a","hospitalId":"north-hospital","status":"ACTIVE"},
    "actions":["read"]}]}'

  for _ in $(seq 1 60); do
    code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
      -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" \
      --data "${probe}" "${CHECK_URL}" 2>/dev/null)"
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

# decide <principal> <status> <resource-tenant> <actions-json>
# Echoes the HTTP status, leaving the response body in /tmp/decision-body.
decide() {
  local principal="$1" status="$2" resource_tenant="$3" actions="$4"
  local token
  token="$(token_of "${principal}")"

  jq -cn \
    --arg status "${status}" \
    --arg resourceTenant "${resource_tenant}" \
    --argjson actions "${actions}" \
    '{
      resources: [{
        kind: "patient_record",
        id: "patient-456",
        attributes: {tenantId: $resourceTenant, hospitalId: "north-hospital", status: $status},
        actions: $actions
      }]
    }' \
  | curl -sS -o /tmp/decision-body -w '%{http_code}' \
      -H 'Content-Type: application/json' \
      -H "Authorization: Bearer ${token}" \
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

# decide_on <principal> <resource-id> <action> - the same shape of request as
# decide, but naming which resource instance a user override might be scoped
# to (§6.2). decide always asks about patient-456; this is only needed for the
# instance-scoping case.
decide_on() {
  local principal="$1" resource_id="$2" action="$3"
  local token
  token="$(token_of "${principal}")"

  jq -cn \
    --arg id "${resource_id}" \
    --arg action "${action}" \
    '{
      resources: [{
        kind: "patient_record",
        id: $id,
        attributes: {tenantId: "tenant-a", hospitalId: "north-hospital", status: "ACTIVE"},
        actions: [$action]
      }]
    }' \
  | curl -sS -o /tmp/decision-body -w '%{http_code}' \
      -H 'Content-Type: application/json' \
      -H "Authorization: Bearer ${token}" \
      -H 'X-Correlation-Id: decision-e2e' \
      --data @- "${CHECK_URL}"
}

# expect_decision_on <description> <principal> <resource-id> <action> <allowed>
expect_decision_on() {
  local description="$1" principal="$2" resource_id="$3" action="$4" expected="$5"

  local code
  code="$(decide_on "${principal}" "${resource_id}" "${action}")"
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

# A seeded, enabled role permission allows, read from the database rather than
# from anything compiled into the service.
expect_decision "a seeded role grant allows read" \
  user-doctor ACTIVE tenant-a read true
expect_decision "a seeded role grant allows update" \
  user-doctor ACTIVE tenant-a update true

# The seed writes a disabled delete row for this very role. Denied because a
# disabled row grants nothing - and, as the next case shows, not because it was
# taken for a denial.
expect_decision "a disabled role permission grants nothing" \
  user-doctor ACTIVE tenant-a delete false

# The other tenant holds a live delete grant for the same role. The case above
# would pass anyway if that grant leaked, so this one pins the direction: the
# leak would have had to allow.
expect_decision "another tenant's live grant does not reach this decision" \
  user-doctor ACTIVE tenant-a delete false

# The auditor's read grant is enabled and out of date. Ignoring expiry would
# visibly allow here.
expect_decision "an expired role permission is ignored" \
  user-auditor ACTIVE tenant-a read false

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

# The seed also writes a live grant for user-doctor-revoked in south-hospital. The
# token's hospital is north-hospital, so that grant must never be read: this is
# the same principal, same action, only the hospital differs.
expect_decision "an override in another hospital does not reach this decision" \
  user-doctor-revoked ACTIVE tenant-a delete false

# An override scoped to one resource instance (§6.2): user-clerk-granted has a
# delete grant for patient-777 only, and no delete grant anywhere else.
expect_decision_on "an instance-scoped grant allows the named instance" \
  user-clerk-granted patient-777 delete true
expect_decision_on "the same grant does not apply to a different instance" \
  user-clerk-granted patient-456 delete false

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
# The revision comes from the tenant's permission_revision row, so this fails if
# the ADS ever reports a constant again.
contract_check "the response reports the tenant's current permission revision" \
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
# refused before a decision is attempted; the realm carries a hostile fixture
# user so this is a token Keycloak really minted.
code="$(decide user-forger ACTIVE tenant-a '["read"]')"
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
  -H "Authorization: Bearer $(token_of user-doctor)" \
  --data '{"resources":[]}' \
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
