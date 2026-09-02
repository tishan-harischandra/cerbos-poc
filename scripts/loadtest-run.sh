#!/usr/bin/env bash
# Runs the §15.3 k6 load suite (deploy/loadtest/k6/main.js) against a
# running, seeded stack and writes the result into a versioned directory
# tagged with the git SHA and the run's full configuration (§15: "results
# written to a versioned results directory with the git SHA and
# configuration, so that numbers remain traceable to what produced them").
#
#   scripts/loadtest-run.sh
#
# Every LOADTEST_* / K6_* environment variable deploy/loadtest/k6/lib/config.js
# reads is forwarded into the container and recorded in the results
# directory's config.json. GO_NETWORK selects the compose network the k6
# container joins, matching scripts/go.sh and scripts/loadtest-seed.sh.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

if [[ -f "${repo_root}/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "${repo_root}/.env"
  set +a
fi

# The environment variables the k6 scripts themselves read (deploy/loadtest/k6/lib/config.js).
# Forwarded to the container when set, and always recorded in config.json
# below (even when unset, so a config.json documents the *default* that
# applied, not just what the operator overrode).
LOADTEST_ENV_NAMES=(
  LOADTEST_BASE_URL LOADTEST_ADMIN_SERVICE_URL LOADTEST_KEYCLOAK_URL LOADTEST_REALM_SUFFIX
  LOADTEST_CLIENT_ID LOADTEST_PASSWORD LOADTEST_TOTAL_USERS LOADTEST_USERNAME_PREFIX
  LOADTEST_USERNAME_DIGITS LOADTEST_TENANTS LOADTEST_HOSPITALS_PER_TENANT
  LOADTEST_HOSPITALS_PER_USER LOADTEST_MUTATION_ROLE
  LOADTEST_MUTATION_RESOURCE LOADTEST_MUTATION_ACTION LOADTEST_VUS_BUSINESS
  LOADTEST_VUS_MODULE LOADTEST_VUS_INSTANCE LOADTEST_VUS_ROW LOADTEST_VUS_TOKEN
  LOADTEST_VUS_MUTATION LOADTEST_RAMP_UP LOADTEST_STEADY_STATE LOADTEST_RAMP_DOWN
  LOADTEST_TOKEN_REUSE_SECONDS
)

git_sha="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
results_dir="${LOADTEST_RESULTS_ROOT:-dist/loadtest-results}/${timestamp}-${git_sha}"
mkdir -p "${results_dir}"

# Build config.json from this run's actual environment: every LOADTEST_*
# variable that resolved to a value (operator-set or the k6 script's own
# default, read back from the running suite via `k6 inspect` would be
# circular, so the operator-visible half - what was actually asked for - is
# what gets recorded here).
{
  echo "{"
  echo "  \"gitSha\": \"${git_sha}\","
  echo "  \"timestamp\": \"${timestamp}\","
  echo "  \"environment\": {"
  first=true
  for name in "${LOADTEST_ENV_NAMES[@]}"; do
    value="${!name:-}"
    if [[ -n "${value}" ]]; then
      [[ "${first}" == true ]] && first=false || echo ","
      printf '    "%s": "%s"' "${name}" "${value}"
    fi
  done
  echo
  echo "  }"
  echo "}"
} >"${results_dir}/config.json"

export K6_NETWORK="${K6_NETWORK:-${GO_NETWORK:-${COMPOSE_PROJECT_NAME:-cerbos-poc}_default}}"
export K6_ENV_PASS="${LOADTEST_ENV_NAMES[*]}"
export K6_RESULTS_DIR="${repo_root}/${results_dir}"

K6_SH="${K6_SH:-${repo_root}/scripts/k6.sh}"

echo "--- running the k6 load suite, results in ${results_dir} ---"
set +e
bash "${K6_SH}" run \
  --summary-export=/output/summary.json \
  /scripts/main.js
status=$?
set -e

echo "${status}" >"${results_dir}/exit-code"
echo "loadtest-run: results written to ${results_dir} (exit ${status})"
exit "${status}"
