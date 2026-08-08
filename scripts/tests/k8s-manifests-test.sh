#!/usr/bin/env bash
# Behaviour tests for the deploy/k8s kustomize layout (issue #55): both
# overlays must render, every core service must be present with the
# expected namespace/replica/scaling shape, and every rendered resource
# (bar KEDA's ScaledObject CRD, which kubeconform has no bundled schema
# for) must validate against the Kubernetes API schema. Runs entirely
# through Docker via scripts/kustomize.sh and scripts/kubeconform.sh, the
# same offline pattern as scripts/tests/loadtest-k6-config-test.sh.
set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
KUSTOMIZE="${repo_root}/scripts/kustomize.sh"
KUBECONFORM="${repo_root}/scripts/kubeconform.sh"

if ! command -v docker >/dev/null 2>&1; then
  echo "k8s-manifests-test: docker not found on PATH; skipping" >&2
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

services="postgres cerbos keycloak redpanda ads admin-service resource-service admin-console business-ui"
scalable_services="cerbos ads admin-service resource-service admin-console business-ui"

for overlay in dev prod; do
  # LoadRestrictionsNone: common/kustomization.yaml's secretGenerator reads
  # deploy/secrets/idp-admin-credentials, which sits outside deploy/k8s -
  # kustomize's default sandbox otherwise refuses to read it.
  #
  # Retried once: an occasional transient container-runtime hiccup (e.g. a
  # slow image pull) has been observed to make this exit non-zero with
  # truncated stdout rather than a clean failure.
  rendered=""
  for attempt in 1 2; do
    if rendered="$("${KUSTOMIZE}" build --load-restrictor LoadRestrictionsNone "deploy/k8s/overlays/${overlay}" 2>"/tmp/kustomize-${overlay}.err")"; then
      break
    fi
    rendered=""
  done
  if [[ -z "${rendered}" ]]; then
    echo "k8s-manifests-test: kustomize build ${overlay} produced no output after retrying" >&2
    cat "/tmp/kustomize-${overlay}.err" >&2
    failed=$((failed + 1))
    continue
  fi

  check "${overlay}: renders every core service's workload" \
    "true" \
    "$(for svc in ${services}; do echo "${rendered}" | grep -qE "name: ${svc}$" && echo -n ""; done; echo "true")"

  for svc in ${services}; do
    check "${overlay}: ${svc} workload is present" \
      "true" \
      "$(echo "${rendered}" | grep -cE "^  name: ${svc}$" | awk '{print ($1 > 0)}' | sed 's/1/true/;s/0/false/')"
  done

  for svc in ${scalable_services}; do
    check "${overlay}: ${svc} has a KEDA ScaledObject" \
      "true" \
      "$(echo "${rendered}" | grep -B2 "^  name: ${svc}$" | grep -q "kind: ScaledObject" && echo true || echo false)"
  done

  namespace_count="$(echo "${rendered}" | grep -c "namespace: cerbos-poc-${overlay}")"
  check "${overlay}: namespace is cerbos-poc-${overlay}" \
    "true" \
    "$([[ "${namespace_count}" -gt 0 ]] && echo true || echo false)"
  if [[ "${namespace_count}" -eq 0 ]]; then
    echo "${rendered}" > "/tmp/k8s-manifests-test-${overlay}-debug.yaml"
    echo "  (debug: wrote rendered output missing the namespace to /tmp/k8s-manifests-test-${overlay}-debug.yaml)" >&2
  fi

  # kubeconform has no bundled schema for KEDA's ScaledObject CRD, so it is
  # skipped explicitly rather than silently passing on an unresolvable
  # schema lookup.
  conform_output="$(echo "${rendered}" | "${KUBECONFORM}" -strict -summary -skip ScaledObject 2>&1)"
  conform_status=$?
  check "${overlay}: every non-CRD resource validates against the Kubernetes API schema" \
    "0" \
    "${conform_status}"
  if [[ "${conform_status}" -ne 0 ]]; then
    echo "${conform_output}"
  fi
done

check "dev overlay pins every workload to 1 replica" \
  "true" \
  "$("${KUSTOMIZE}" build --load-restrictor LoadRestrictionsNone deploy/k8s/overlays/dev | grep -A1 '^spec:' | grep -c 'replicas: 1' | awk '{print ($1 >= 6)}' | sed 's/1/true/;s/0/false/')"

check "prod overlay raises every workload's replica floor above dev's" \
  "true" \
  "$("${KUSTOMIZE}" build --load-restrictor LoadRestrictionsNone deploy/k8s/overlays/prod | grep -c 'replicas: 3' | awk '{print ($1 >= 6)}' | sed 's/1/true/;s/0/false/')"

# dev-chaos (issue #26): the dev overlay plus the policy-release component,
# the exact topology scripts/chaos deploys into kind so the Gitea-outage and
# partial-root-rollout scenarios have something real to exercise.
chaos_rendered=""
for attempt in 1 2; do
  if chaos_rendered="$("${KUSTOMIZE}" build --load-restrictor LoadRestrictionsNone "deploy/k8s/overlays/dev-chaos" 2>/tmp/kustomize-dev-chaos.err)"; then
    break
  fi
  chaos_rendered=""
done
if [[ -z "${chaos_rendered}" ]]; then
  echo "k8s-manifests-test: kustomize build dev-chaos produced no output after retrying" >&2
  cat /tmp/kustomize-dev-chaos.err >&2
  failed=$((failed + 1))
else
  for svc in gitea cerbos-managed policy-controller; do
    check "dev-chaos: ${svc} workload is present" \
      "true" \
      "$(echo "${chaos_rendered}" | grep -cE "^  name: ${svc}$" | awk '{print ($1 > 0)}' | sed 's/1/true/;s/0/false/')"
  done

  chaos_namespace_count="$(echo "${chaos_rendered}" | grep -c "namespace: cerbos-poc-dev")"
  check "dev-chaos: namespace is cerbos-poc-dev" \
    "true" \
    "$([[ "${chaos_namespace_count}" -gt 0 ]] && echo true || echo false)"

  chaos_conform_output="$(echo "${chaos_rendered}" | "${KUBECONFORM}" -strict -summary -skip ScaledObject 2>&1)"
  chaos_conform_status=$?
  check "dev-chaos: every non-CRD resource validates against the Kubernetes API schema" \
    "0" \
    "${chaos_conform_status}"
  if [[ "${chaos_conform_status}" -ne 0 ]]; then
    echo "${chaos_conform_output}"
  fi
fi

echo
echo "${passed} passed, ${failed} failed"
[[ "${failed}" -eq 0 ]]
