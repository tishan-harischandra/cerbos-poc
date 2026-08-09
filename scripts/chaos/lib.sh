#!/usr/bin/env bash
# Shared configuration and helpers for the issue #26 chaos suite. Sourced by
# cluster-up.sh, run.sh and every scripts/chaos/scenarios/*.sh.
set -uo pipefail

CHAOS_CLUSTER_NAME="${CHAOS_CLUSTER_NAME:-cerbos-poc-chaos}"
CHAOS_NAMESPACE="${CHAOS_NAMESPACE:-cerbos-poc-dev}"
CHAOS_OVERLAY="${CHAOS_OVERLAY:-deploy/k8s/overlays/dev-chaos}"
KEDA_VERSION="${KEDA_VERSION:-2.15.1}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Override only to point at a different node topology than
# scripts/chaos/kind-config.yaml's three nodes - e.g. a single-node config
# on a docker setup that cannot join kind worker nodes (see the note in
# 08-node-drain.sh).
CHAOS_KIND_CONFIG="${CHAOS_KIND_CONFIG:-${repo_root}/scripts/chaos/kind-config.yaml}"
CHAOS_KUBECONFIG="${CHAOS_KUBECONFIG:-${repo_root}/.chaos-kubeconfig}"

DOCKER="${DOCKER:-docker}"
CHAOS_TOOLS_DIR="${CHAOS_TOOLS_DIR:-${repo_root}/.chaos-tools}"
KIND="${KIND:-${CHAOS_TOOLS_DIR}/kind}"
KUBECTL="${KUBECTL:-${CHAOS_TOOLS_DIR}/kubectl}"
KUSTOMIZE="${repo_root}/scripts/kustomize.sh"
KIND_VERSION="${KIND_VERSION:-v0.23.0}"
KUBECTL_VERSION="${KUBECTL_VERSION:-v1.30.0}"

# install_tools downloads pinned kind and kubectl binaries into
# CHAOS_TOOLS_DIR if they are not already there. Neither ships as a
# container image that can drive the host's own docker socket the way
# scripts/kustomize.sh and scripts/kubeconform.sh's tools do, so - unlike
# every other tool in this repo - these two are the one place a real binary
# lands on the host, gitignored, the same way node_modules/ is.
install_tools() {
  mkdir -p "${CHAOS_TOOLS_DIR}"
  if [[ ! -x "${KIND}" ]]; then
    echo "==> Downloading kind ${KIND_VERSION}"
    curl -sSL -o "${KIND}" "https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-linux-amd64"
    chmod +x "${KIND}"
  fi
  if [[ ! -x "${KUBECTL}" ]]; then
    echo "==> Downloading kubectl ${KUBECTL_VERSION}"
    curl -sSL -o "${KUBECTL}" "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl"
    chmod +x "${KUBECTL}"
  fi
}

# Every decision-e2e.sh/lib-token.sh default lines up with these forwarded
# ports, so both are reused unmodified once the port-forwards below are up:
# Keycloak's KC_HOSTNAME is baked in as http://localhost:8081 (see
# deploy/k8s/base/keycloak/deployment.yaml), and ADMIN_CONSOLE_PORT's own
# default is 4200.
export KEYCLOAK_PORT="${KEYCLOAK_PORT:-8081}"
export ADMIN_CONSOLE_PORT="${ADMIN_CONSOLE_PORT:-4200}"
CHAOS_POSTGRES_PORT="${CHAOS_POSTGRES_PORT:-5432}"
CHAOS_GITEA_PORT="${CHAOS_GITEA_PORT:-3001}"

kubectl_chaos() {
  KUBECONFIG="${CHAOS_KUBECONFIG}" "${KUBECTL}" --context "kind-${CHAOS_CLUSTER_NAME}" -n "${CHAOS_NAMESPACE}" "$@"
}

kubectl_chaos_sys() {
  KUBECONFIG="${CHAOS_KUBECONFIG}" "${KUBECTL}" --context "kind-${CHAOS_CLUSTER_NAME}" "$@"
}

# wait_for_rollout <kind>/<name> [timeout]
wait_for_rollout() {
  local target="$1" timeout="${2:-300s}"
  kubectl_chaos rollout status "${target}" --timeout="${timeout}"
}

# start_port_forwards backgrounds the four port-forwards every scenario and
# the setup migration/seed steps need, and records their PIDs in
# ${CHAOS_PORT_FORWARD_PIDS_FILE} so stop_port_forwards can clean them up.
CHAOS_PORT_FORWARD_PIDS_FILE="${repo_root}/.chaos-port-forward-pids"

# start_one_port_forward <target> <local_port> <remote_port>
# Backgrounds a single kubectl port-forward tunnel and records its PID in
# CHAOS_PORT_FORWARD_PIDS_FILE so stop_port_forwards cleans it up too.
start_one_port_forward() {
  local target="$1" local_port="$2" remote_port="$3"
  kubectl_chaos port-forward "${target}" "${local_port}:${remote_port}" \
    >>"${repo_root}/.chaos-port-forward.log" 2>&1 &
  echo "$!" >> "${CHAOS_PORT_FORWARD_PIDS_FILE}"
}

start_port_forwards() {
  : > "${CHAOS_PORT_FORWARD_PIDS_FILE}"
  local spec
  for spec in \
    "svc/postgres:${CHAOS_POSTGRES_PORT}:5432" \
    "svc/keycloak:${KEYCLOAK_PORT}:8080" \
    "svc/admin-console:${ADMIN_CONSOLE_PORT}:80" \
    "svc/gitea:${CHAOS_GITEA_PORT}:3000"; do
    local target="${spec%%:*}" rest="${spec#*:}"
    local local_port="${rest%%:*}" remote_port="${rest#*:}"
    start_one_port_forward "${target}" "${local_port}" "${remote_port}"
  done
  # Give kubectl a moment to establish the tunnels before the first caller
  # tries to use one.
  sleep 3
}

# restart_port_forward <target> <local_port> <remote_port>
# kubectl port-forward against a Service resolves to one specific pod at
# the moment the tunnel opens and never follows a pod replacement. Any
# scenario that deletes or replaces the pod behind one of
# start_port_forwards' tunnels (scaling a deployment to 0 and back, for
# example) leaves that tunnel forwarding to a pod that no longer exists,
# breaking every later scenario that reuses it. Call this right after such
# a scenario restores the deployment.
restart_port_forward() {
  local target="$1" local_port="$2" remote_port="$3"
  pkill -f "port-forward ${target} ${local_port}:${remote_port}" 2>/dev/null || true
  start_one_port_forward "${target}" "${local_port}" "${remote_port}"
  sleep 3
}

stop_port_forwards() {
  [[ -f "${CHAOS_PORT_FORWARD_PIDS_FILE}" ]] || return 0
  local pid
  while read -r pid; do
    kill "${pid}" 2>/dev/null || true
  done < "${CHAOS_PORT_FORWARD_PIDS_FILE}"
  rm -f "${CHAOS_PORT_FORWARD_PIDS_FILE}"
}

# decision_probe_loop <duration-seconds> <failure-count-file>
# Sends one authorization decision per second through admin-console -> ADS ->
# Cerbos for the given duration, appending one line ("ok" or "FAIL <code>")
# per request to failure-count-file. Scenarios run this in the background
# while they inject a failure, then assert no FAIL line was written.
decision_probe_loop() {
  local duration="$1" out_file="$2"
  # shellcheck source=scripts/tests/lib-token.sh
  source "${repo_root}/scripts/tests/lib-token.sh"
  local check_url="http://127.0.0.1:${ADMIN_CONSOLE_PORT}/api/ads/internal/authz/check"
  local token
  token="$(token_for user-unassigned)" || { echo "FAIL token" >> "${out_file}"; return 1; }
  local probe='{"resources":[{"kind":"patient_record","id":"patient-000",
    "attributes":{"tenantId":"tenant-a","hospitalId":"hospital-1","status":"ACTIVE"},
    "actions":["read"]}]}'
  local end=$((SECONDS + duration))
  while (( SECONDS < end )); do
    local code
    code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
      -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" \
      --data "${probe}" "${check_url}" 2>/dev/null)"
    if [[ "${code}" == "200" ]]; then
      echo "ok" >> "${out_file}"
    else
      echo "FAIL ${code}" >> "${out_file}"
    fi
    sleep 1
  done
}

# pin_to_one_replica <deployment-name>
# ads's role-matrix and JWKS caches are both per replica, not shared, so a
# scenario that warms a fact on one replica and asserts on it later needs
# every request in between to land on that same replica. Echoes the
# deployment's original replica count and registers an EXIT trap that
# restores it; callers only need to capture that count if they want to log
# it, restore_one_replica below is registered automatically.
pin_to_one_replica() {
  local deployment="$1"
  local original
  original="$(kubectl_chaos get "deployment/${deployment}" -o jsonpath='{.spec.replicas}')"
  if [[ "${original}" != "1" ]]; then
    echo "==> Pinning ${deployment} to one replica for this scenario (was ${original})"
    kubectl_chaos scale "deployment/${deployment}" --replicas=1
    wait_for_rollout "deployment/${deployment}" 120s
  fi
  # shellcheck disable=SC2064
  trap "restore_replicas '${deployment}' '${original}'" EXIT
}

restore_replicas() {
  local deployment="$1" original="$2"
  if [[ "${original}" != "1" ]]; then
    kubectl_chaos scale "deployment/${deployment}" --replicas="${original}"
  fi
}

# assert_no_failures <failure-count-file> <description>
assert_no_failures() {
  local out_file="$1" description="$2"
  if [[ ! -s "${out_file}" ]]; then
    echo "FAIL ${description} (probe recorded no requests at all)"
    return 1
  fi
  local failures
  failures="$(grep -c '^FAIL' "${out_file}" || true)"
  if [[ "${failures}" -eq 0 ]]; then
    echo "ok   ${description} ($(wc -l < "${out_file}") decisions, 0 failed)"
    return 0
  fi
  echo "FAIL ${description} (${failures} of $(wc -l < "${out_file}") decisions failed)"
  grep '^FAIL' "${out_file}" | sort | uniq -c
  return 1
}
