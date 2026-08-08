#!/usr/bin/env bash
# Behaviour tests for the k6 load suite's structure (issue #25): scenario
# mix, thresholds and total concurrency. Runs `k6 inspect`, which parses the
# script's exported `options` without ever making a network call, so this is
# offline with respect to any running stack - it only needs Docker and the
# k6 image scripts/k6.sh already pins.
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
K6="${repo_root}/scripts/k6.sh"

if ! command -v docker >/dev/null 2>&1; then
  echo "loadtest-k6-config-test: docker not found on PATH; skipping" >&2
  exit 0
fi

passed=0
failed=0

check() {
  local name="$1" expected="$2" actual="$3"
  if [[ "${expected}" == "${actual}" ]]; then
    printf 'ok   %s\n' "${name}"
    passed=$((passed + 1))
  else
    printf 'FAIL %s\n     expected: %s\n     actual:   %s\n' "${name}" "${expected}" "${actual}"
    failed=$((failed + 1))
  fi
}

inspect_output="$("${K6}" inspect --execution-requirements /scripts/main.js 2>/dev/null)"
if [[ -z "${inspect_output}" ]]; then
  echo "loadtest-k6-config-test: k6 inspect produced no output" >&2
  exit 1
fi

check "the run sustains 1,000 concurrent virtual users" \
  "1000" \
  "$(jq -r '.maxVUs' <<<"${inspect_output}")"

check "the business endpoint authorization scenario is present" \
  "true" \
  "$(jq -r 'has("business_authz")' <<<"$(jq '.scenarios' <<<"${inspect_output}")")"

check "all three capability snapshot shapes are present in the same run" \
  "capability_instance
capability_module
capability_row" \
  "$(jq -r '.scenarios | keys[]' <<<"${inspect_output}" | grep '^capability_' | sort)"

check "the token endpoint baseline scenario is present" \
  "true" \
  "$(jq -r 'has("token_baseline")' <<<"$(jq '.scenarios' <<<"${inspect_output}")")"

check "the mid-run mutation/convergence scenario is present" \
  "true" \
  "$(jq -r 'has("mutation_convergence")' <<<"$(jq '.scenarios' <<<"${inspect_output}")")"

check "warm decision latency is enforced at p95<15ms and p99<30ms" \
  '["p(95)<15","p(99)<30"]' \
  "$(jq -c '.thresholds.warm_decision_latency_ms' <<<"${inspect_output}")"

check "permission convergence is enforced within 5 seconds at p99" \
  '["p(99)<5000"]' \
  "$(jq -c '.thresholds.permission_convergence_ms' <<<"${inspect_output}")"

check "revocation convergence is enforced within 5 seconds at p99" \
  '["p(99)<5000"]' \
  "$(jq -c '.thresholds.revocation_convergence_ms' <<<"${inspect_output}")"

check "no business operation proceeding without an explicit allow is a hard threshold" \
  '["count==0"]' \
  "$(jq -c '.thresholds.business_op_without_allow' <<<"${inspect_output}")"

check "every scenario defines a ramp-up, steady state and ramp-down" \
  "true" \
  "$(jq -r '[.scenarios[].stages | length == 3] | all' <<<"${inspect_output}")"

check "the ramp-down stage always targets zero VUs" \
  "true" \
  "$(jq -r '[.scenarios[].stages[-1].target == 0] | all' <<<"${inspect_output}")"

echo
echo "${passed} passed, ${failed} failed"
[[ "${failed}" -eq 0 ]]
