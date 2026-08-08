#!/usr/bin/env bash
# Behaviour tests for the loadtest results-directory wiring: versioned
# naming, git-SHA tagging, recorded configuration and exit-code propagation.
# The real k6 invocation is stubbed out (K6_SH), so this never touches
# Docker, a network or a running stack.
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RUN_SCRIPT="${repo_root}/scripts/loadtest-run.sh"

passed=0
failed=0
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

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

fake_k6() {
  local exit_code="$1" path="${tmp}/fake-k6-${exit_code}.sh"
  cat >"${path}" <<EOF
#!/usr/bin/env bash
exit ${exit_code}
EOF
  chmod +x "${path}"
  printf '%s' "${path}"
}

run_with() {
  local exit_code="$1" results_root="$2"
  K6_SH="$(fake_k6 "${exit_code}")" \
  LOADTEST_RESULTS_ROOT="${results_root}" \
  GO_NETWORK="test-network" \
  LOADTEST_TENANT_ID="tenant-test" \
    bash "${RUN_SCRIPT}" >/dev/null 2>&1
  echo "$?"
}

results_root_pass="${tmp}/results-pass"
check "a successful k6 run exits zero" \
  "0" \
  "$(run_with 0 "${results_root_pass}")"

result_dir="$(find "${results_root_pass}" -mindepth 1 -maxdepth 1 -type d | head -n1)"

check "a results directory was created under the versioned root" \
  "true" \
  "$([[ -n "${result_dir}" ]] && echo true || echo false)"

check "the results directory name is tagged with the current git SHA" \
  "true" \
  "$(git_sha=$(cd "${repo_root}" && git rev-parse --short HEAD)
     [[ "${result_dir}" == *"-${git_sha}" ]] && echo true || echo false)"

check "config.json records the run's configuration" \
  "tenant-test" \
  "$(jq -r '.environment.LOADTEST_TENANT_ID' "${result_dir}/config.json" 2>/dev/null)"

check "config.json records the git SHA" \
  "true" \
  "$(jq -e '.gitSha | length > 0' "${result_dir}/config.json" >/dev/null 2>&1 && echo true || echo false)"

check "the exit code is recorded alongside the results" \
  "0" \
  "$(cat "${result_dir}/exit-code" 2>/dev/null)"

results_root_fail="${tmp}/results-fail"
check "a threshold breach propagates a non-zero exit code" \
  "1" \
  "$(run_with 1 "${results_root_fail}")"

fail_dir="$(find "${results_root_fail}" -mindepth 1 -maxdepth 1 -type d | head -n1)"
check "the failing run's exit code is still recorded" \
  "1" \
  "$(cat "${fail_dir}/exit-code" 2>/dev/null)"

echo
echo "${passed} passed, ${failed} failed"
[[ "${failed}" -eq 0 ]]
