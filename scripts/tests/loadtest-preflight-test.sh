#!/usr/bin/env bash
# Behaviour tests for the loadtest host preflight check. Runs entirely
# offline against fake meminfo/disk paths; never touches the real host's
# resources or a running stack.
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PREFLIGHT="${repo_root}/scripts/loadtest-preflight.sh"

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

meminfo_with() {
  local kb="$1"
  local path="${tmp}/meminfo-${kb}"
  printf 'MemTotal:       16384000 kB\nMemAvailable:   %s kB\n' "${kb}" >"${path}"
  printf '%s' "${path}"
}

# A directory whose free space `df` reports as plenty, for tests that only
# want to exercise the memory branch.
roomy_disk="${tmp}"

run_preflight() {
  LOADTEST_PREFLIGHT_MEMINFO="$1" \
  LOADTEST_PREFLIGHT_DISK_PATH="$2" \
  LOADTEST_MIN_FREE_MEM_KB="${3:-8388608}" \
  LOADTEST_MIN_FREE_DISK_KB="${4:-10485760}" \
  "${PREFLIGHT}" >/dev/null 2>&1
  echo "$?"
}

check "a host with plenty of free memory and disk passes" \
  "0" \
  "$(run_preflight "$(meminfo_with 16000000)" "${roomy_disk}")"

check "a host below the memory minimum refuses" \
  "1" \
  "$(run_preflight "$(meminfo_with 1000)" "${roomy_disk}")"

check "a host below the disk minimum refuses" \
  "1" \
  "$(run_preflight "$(meminfo_with 16000000)" "${roomy_disk}" 8388608 999999999999)"

check "the memory threshold is configurable" \
  "0" \
  "$(run_preflight "$(meminfo_with 2000)" "${roomy_disk}" 1000)"

printf 'MemTotal: 100 kB\n' >"${tmp}/no-available"
no_available_output="$(LOADTEST_PREFLIGHT_MEMINFO="${tmp}/no-available" LOADTEST_PREFLIGHT_DISK_PATH="${roomy_disk}" \
  "${PREFLIGHT}" 2>&1)"
if grep -q 'no MemAvailable line' <<<"${no_available_output}"; then
  no_available_result=true
else
  no_available_result=false
fi
check "a meminfo file with no MemAvailable line refuses with a clear message" \
  "true" "${no_available_result}"

check "an unreadable meminfo path refuses rather than crashing" \
  "1" \
  "$(run_preflight "${tmp}/does-not-exist" "${roomy_disk}")"

echo
echo "${passed} passed, ${failed} failed"
[[ "${failed}" -eq 0 ]]
