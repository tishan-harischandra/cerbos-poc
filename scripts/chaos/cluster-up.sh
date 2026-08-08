#!/usr/bin/env bash
# Brings up a real three-node kind cluster running the dev-chaos overlay
# (issue #26): installs KEDA, builds and loads every service image, applies
# the overlay, migrates and seeds the database, seeds Gitea with the root
# policy tag, and waits for the decision path to answer. Idempotent: safe to
# re-run against an already-up cluster.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"

if ! command -v "${DOCKER}" >/dev/null 2>&1; then
  echo "scripts/chaos/cluster-up.sh: '${DOCKER}' not found on PATH" >&2
  exit 127
fi

install_tools

if ! "${KIND}" get clusters | grep -qx "${CHAOS_CLUSTER_NAME}"; then
  echo "==> Creating kind cluster ${CHAOS_CLUSTER_NAME}"
  "${KIND}" create cluster --name "${CHAOS_CLUSTER_NAME}" --config "${CHAOS_KIND_CONFIG}"
else
  echo "==> Reusing existing kind cluster ${CHAOS_CLUSTER_NAME}"
fi
"${KIND}" get kubeconfig --name "${CHAOS_CLUSTER_NAME}" > "${CHAOS_KUBECONFIG}"

echo "==> Installing metrics-server (every ScaledObject here is a cpu trigger, which reads the metrics.k8s.io API)"
kubectl_chaos_sys apply -f "https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml"
# kind's kubelet certificate is not signed for a name metrics-server
# validates by default.
kubectl_chaos_sys -n kube-system patch deployment metrics-server --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
kubectl_chaos_sys -n kube-system rollout status deployment/metrics-server --timeout=180s

echo "==> Installing KEDA ${KEDA_VERSION}"
kubectl_chaos_sys apply --server-side -f "https://github.com/kedacore/keda/releases/download/v${KEDA_VERSION}/keda-${KEDA_VERSION}.yaml"
kubectl_chaos_sys -n keda rollout status deployment/keda-operator --timeout=180s
kubectl_chaos_sys -n keda rollout status deployment/keda-metrics-apiserver --timeout=180s

echo "==> Building service images"
declare -A images=(
  [cerbos-poc/cerbos-assets:dev]="deploy/cerbos/Dockerfile"
  [cerbos-poc/ads:dev]="apps/ads/Dockerfile"
  [cerbos-poc/admin-service:dev]="apps/admin-service/Dockerfile"
  [cerbos-poc/resource-service:dev]="apps/resource-service/Dockerfile"
  [cerbos-poc/admin-console:dev]="apps/admin-console/Dockerfile"
  [cerbos-poc/business-ui:dev]="apps/business-ui/Dockerfile"
  [cerbos-poc/policy-controller:dev]="apps/policy-controller/Dockerfile"
)
for tag in "${!images[@]}"; do
  echo "    ${tag}"
  "${DOCKER}" build -q -t "${tag}" -f "${repo_root}/${images[${tag}]}" "${repo_root}" >/dev/null
done

echo "==> Loading images into ${CHAOS_CLUSTER_NAME}"
for tag in "${!images[@]}"; do
  "${KIND}" load docker-image "${tag}" --name "${CHAOS_CLUSTER_NAME}"
done

echo "==> Creating namespace ${CHAOS_NAMESPACE}"
kubectl_chaos_sys create namespace "${CHAOS_NAMESPACE}" --dry-run=client -o yaml | kubectl_chaos_sys apply -f -

echo "==> Applying ${CHAOS_OVERLAY}"
"${KUSTOMIZE}" build --load-restrictor LoadRestrictionsNone "${CHAOS_OVERLAY}" | kubectl_chaos_sys apply -f -

echo "==> Waiting for workloads"
for kind_name in \
  statefulset/postgres \
  statefulset/redpanda \
  deployment/keycloak \
  deployment/cerbos \
  deployment/ads \
  deployment/admin-service \
  deployment/resource-service \
  deployment/admin-console \
  deployment/business-ui \
  deployment/gitea \
  deployment/policy-controller; do
  wait_for_rollout "${kind_name}" 300s
done

start_port_forwards
trap stop_port_forwards EXIT

echo "==> Applying the assignmentstore schema"
GO_NETWORK=host LIQUIBASE_NETWORK=host \
  POSTGRES_HOST=127.0.0.1 POSTGRES_PORT="${CHAOS_POSTGRES_PORT}" \
  bash "${repo_root}/scripts/liquibase.sh" postgres update

echo "==> Seeding the demo role matrix"
GO_NETWORK=host \
  POSTGRES_HOST=127.0.0.1 POSTGRES_PORT="${CHAOS_POSTGRES_PORT}" \
  bash "${repo_root}/scripts/seed.sh" postgres

echo "==> Seeding Gitea with the root policy tag"
bash "${repo_root}/scripts/chaos/gitea-seed.sh"

echo "==> Waiting for the decision path"
# shellcheck source=scripts/tests/lib-token.sh
source "${repo_root}/scripts/tests/lib-token.sh"
wait_for_keycloak
token="$(token_for user-unassigned)"
check_url="http://127.0.0.1:${ADMIN_CONSOLE_PORT}/api/ads/internal/authz/check"
probe='{"resources":[{"kind":"patient_record","id":"patient-000",
  "attributes":{"tenantId":"tenant-a","hospitalId":"hospital-1","status":"ACTIVE"},
  "actions":["read"]}]}'
ready=0
for _ in $(seq 1 60); do
  code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
    -H 'Content-Type: application/json' -H "Authorization: Bearer ${token}" \
    --data "${probe}" "${check_url}" 2>/dev/null)"
  if [[ "${code}" == "200" ]]; then
    ready=1
    break
  fi
  sleep 1
done
trap - EXIT
if [[ "${ready}" -ne 1 ]]; then
  echo "scripts/chaos/cluster-up.sh: the decision path never came up (last HTTP ${code:-none})" >&2
  exit 1
fi

echo "==> Cluster ${CHAOS_CLUSTER_NAME} is ready"
