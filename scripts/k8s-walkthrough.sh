#!/usr/bin/env bash
# The same guided walkthrough, against the deploy/k8s dev overlay on a real
# kind cluster instead of docker compose (issue #27).
#
# A README that claims two deployment paths has to be checked on both, or the
# Kubernetes half is a hypothesis. This stands up a single-node kind cluster
# running overlays/dev, migrates and seeds it exactly as compose does, and
# then runs scripts/tests/walkthrough.sh unchanged through port-forwards - the
# same script, the same assertions, a different deployment.
#
#   bash scripts/k8s-walkthrough.sh          # up, walkthrough, tear down
#   K8S_WALKTHROUGH_KEEP=1 bash scripts/k8s-walkthrough.sh   # leave it running
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Reuse the chaos suite's cluster helpers, pointed at their own cluster,
# namespace and overlay so this never collides with a chaos run.
export CHAOS_CLUSTER_NAME="${K8S_WALKTHROUGH_CLUSTER:-cerbos-poc-dev}"
export CHAOS_NAMESPACE="${K8S_WALKTHROUGH_NAMESPACE:-cerbos-poc-dev}"
export CHAOS_OVERLAY="${K8S_WALKTHROUGH_OVERLAY:-deploy/k8s/overlays/dev}"
export CHAOS_KUBECONFIG="${CHAOS_KUBECONFIG:-${repo_root}/.k8s-walkthrough-kubeconfig}"
export CHAOS_KIND_CONFIG="${CHAOS_KIND_CONFIG:-${repo_root}/scripts/k8s-walkthrough-kind.yaml}"
# Not 5432. A developer machine running this very likely already has something
# on the default PostgreSQL port - a host install, or the compose stack - and
# `kubectl port-forward` does not fail when it cannot bind 127.0.0.1: it binds
# ::1 only and looks fine, so the migration silently authenticates against the
# wrong database and reports a password failure.
export CHAOS_POSTGRES_PORT="${CHAOS_POSTGRES_PORT:-55432}"

# shellcheck source=scripts/chaos/lib.sh
source "${repo_root}/scripts/chaos/lib.sh"

command -v "${DOCKER}" >/dev/null 2>&1 || {
  echo "k8s-walkthrough: '${DOCKER}' not found on PATH" >&2; exit 127; }

install_tools

if ! "${KIND}" get clusters | grep -qx "${CHAOS_CLUSTER_NAME}"; then
  echo "==> Creating kind cluster ${CHAOS_CLUSTER_NAME}"
  "${KIND}" create cluster --name "${CHAOS_CLUSTER_NAME}" --config "${CHAOS_KIND_CONFIG}"
else
  echo "==> Reusing existing kind cluster ${CHAOS_CLUSTER_NAME}"
fi
"${KIND}" get kubeconfig --name "${CHAOS_CLUSTER_NAME}" > "${CHAOS_KUBECONFIG}"

teardown() {
  stop_port_forwards
  if [[ "${K8S_WALKTHROUGH_KEEP:-0}" != "1" ]]; then
    echo "==> Deleting kind cluster ${CHAOS_CLUSTER_NAME}"
    "${KIND}" delete cluster --name "${CHAOS_CLUSTER_NAME}" >/dev/null 2>&1 || true
  else
    echo "==> Leaving ${CHAOS_CLUSTER_NAME} up (K8S_WALKTHROUGH_KEEP=1)"
  fi
}
trap teardown EXIT

# Every base workload carries a KEDA ScaledObject, so the CRD has to exist
# before the overlay applies - the same prerequisite the README names for a
# real cluster.
echo "==> Installing KEDA ${KEDA_VERSION}"
kubectl_chaos_sys apply --server-side -f \
  "https://github.com/kedacore/keda/releases/download/v${KEDA_VERSION}/keda-${KEDA_VERSION}.yaml"
kubectl_chaos_sys -n keda rollout status deployment/keda-operator --timeout=300s

# Fully qualified on purpose. The manifests name `cerbos-poc/<x>:dev`, which
# containerd resolves as `docker.io/cerbos-poc/<x>:dev`; podman would otherwise
# build and load the same image as `localhost/cerbos-poc/<x>:dev`, and every
# pod would sit in ImagePullBackOff next to an image that is already there.
# Docker treats the qualified name as the same image, so this costs nothing.
echo "==> Building service images"
declare -A images=(
  [docker.io/cerbos-poc/cerbos-assets:dev]="deploy/cerbos/Dockerfile"
  [docker.io/cerbos-poc/ads:dev]="apps/ads/Dockerfile"
  [docker.io/cerbos-poc/admin-service:dev]="apps/admin-service/Dockerfile"
  [docker.io/cerbos-poc/resource-service:dev]="apps/resource-service/Dockerfile"
  [docker.io/cerbos-poc/business-ui:dev]="apps/business-ui/Dockerfile"
  # The organization selector authenticator (issue #79) is baked into this
  # image at build time, the same way docker-compose.yml builds it.
  [docker.io/cerbos-poc/keycloak:dev]="apps/keycloak-org-selector/Dockerfile"
)
for tag in "${!images[@]}"; do
  echo "    ${tag}"
  "${DOCKER}" build -q -t "${tag}" -f "${repo_root}/${images[${tag}]}" "${repo_root}" >/dev/null
done

echo "==> Loading images into ${CHAOS_CLUSTER_NAME}"
for tag in "${!images[@]}"; do
  "${KIND}" load docker-image "${tag}" --name "${CHAOS_CLUSTER_NAME}"
done

echo "==> Applying ${CHAOS_OVERLAY}"
kubectl_chaos_sys create namespace "${CHAOS_NAMESPACE}" --dry-run=client -o yaml \
  | kubectl_chaos_sys apply -f -
"${KUSTOMIZE}" build --load-restrictor LoadRestrictionsNone "${CHAOS_OVERLAY}" \
  | kubectl_chaos_sys apply -f -

echo "==> Waiting for infrastructure workloads"
for target in \
  statefulset/postgres \
  statefulset/redpanda \
  deployment/keycloak \
  deployment/cerbos; do
  # Generous, because the first run on a fresh cluster pulls PostgreSQL,
  # Keycloak and Redpanda from a registry while it waits. A subsequent run
  # against a reused cluster settles in well under a minute.
  wait_for_rollout "${target}" "${K8S_WALKTHROUGH_ROLLOUT_TIMEOUT:-900s}"
done

# ads, admin-service and resource-service resolve their tenant from the
# tenant registry at startup (issue #76) and will not become Ready until
# it is seeded, so the schema, the role matrix and the tenant registry all
# have to be in place before those workloads are even waited on - the same
# ordering constraint `make up` observes for compose. A lone port-forward to
# Postgres is enough for that; the rest come up once start_port_forwards
# runs below.
start_one_port_forward svc/postgres "${CHAOS_POSTGRES_PORT}" 5432
sleep 3

echo "==> Applying the assignmentstore schema"
GO_NETWORK=host LIQUIBASE_NETWORK=host \
  POSTGRES_HOST=127.0.0.1 POSTGRES_PORT="${CHAOS_POSTGRES_PORT}" \
  bash "${repo_root}/scripts/liquibase.sh" postgres update

echo "==> Seeding the demo role matrix"
GO_NETWORK=host \
  POSTGRES_HOST=127.0.0.1 POSTGRES_PORT="${CHAOS_POSTGRES_PORT}" \
  bash "${repo_root}/scripts/seed.sh" postgres

echo "==> Seeding the tenant registry"
GO_NETWORK=host \
  POSTGRES_HOST=127.0.0.1 POSTGRES_PORT="${CHAOS_POSTGRES_PORT}" \
  bash "${repo_root}/scripts/seed-tenants.sh" postgres

stop_port_forwards

echo "==> Waiting for application workloads"
for target in \
  deployment/ads \
  deployment/admin-service \
  deployment/resource-service \
  deployment/business-ui; do
  wait_for_rollout "${target}" "${K8S_WALKTHROUGH_ROLLOUT_TIMEOUT:-900s}"
done

start_port_forwards

echo "==> Running the guided walkthrough against the cluster"
bash "${repo_root}/scripts/tests/walkthrough.sh"
