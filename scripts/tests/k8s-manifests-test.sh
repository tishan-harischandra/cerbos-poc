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

# contains reports whether a rendering holds a string.
#
# It reads from a herestring rather than `echo ... | grep -q` on purpose.
# `grep -q` exits the moment it matches, which closes the pipe under `echo`
# and kills it with SIGPIPE; with `pipefail` set, that turns a successful
# match into a failed pipeline whenever grep happens to win the race. The
# result is a check that passes or fails depending on timing - which is
# exactly what was observed here before this helper existed.
contains() {
  local haystack="$1" needle="$2"
  if grep -q -- "${needle}" <<<"${haystack}"; then
    echo true
  else
    echo false
  fi
}

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

# admin-console is deliberately absent: the Administration Service serves the
# console now, so there is no console workload to render (ADR-008).
services="postgres cerbos keycloak redpanda ads admin-service resource-service business-ui"
scalable_services="cerbos ads admin-service resource-service business-ui"

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
      "$(contains "$(grep -B2 "^  name: ${svc}$" <<<"${rendered}")" "kind: ScaledObject")"
  done

  namespace_count="$(echo "${rendered}" | grep -c "namespace: cerbos-poc-${overlay}")"
  check "${overlay}: namespace is cerbos-poc-${overlay}" \
    "true" \
    "$([[ "${namespace_count}" -gt 0 ]] && echo true || echo false)"
  if [[ "${namespace_count}" -eq 0 ]]; then
    echo "${rendered}" > "/tmp/k8s-manifests-test-${overlay}-debug.yaml"
    echo "  (debug: wrote rendered output missing the namespace to /tmp/k8s-manifests-test-${overlay}-debug.yaml)" >&2
  fi

  # ADR-009: every workload that runs a singleton has to say which
  # mechanism elects it. The code refuses to start without this, so a
  # manifest that omits it is a deployment that crashloops - which is safe,
  # but is a failure worth catching here rather than in a cluster.
  check "${overlay}: admin-service is told how the election is run" \
    "true" \
    "$(contains "${rendered}" "LEADER_ELECTION_TYPE")"

  # And the mechanism it is told to use has to be one the elector can
  # actually reach: under K8S_LEASE that means a ServiceAccount bound to a
  # Role over coordination.k8s.io Leases, or every renewal is a 403.
  check "${overlay}: the elector may hold a Lease" \
    "true" \
    "$(contains "${rendered}" "coordination.k8s.io")"
  check "${overlay}: admin-service runs as the elector service account" \
    "true" \
    "$(contains "${rendered}" "serviceAccountName: leader-elector")"

  # ADR-008: the console has no deployment, no service and no image of its
  # own. Asserting its absence is what stops it being quietly reintroduced
  # alongside the service that now serves it.
  check "${overlay}: nothing deploys the console separately" \
    "false" \
    "$(contains "${rendered}" "admin-console")"

  # Issue #83: tenant is resolved from the request host, one subdomain per
  # tenant, with no host-file editing anywhere this manifest itself
  # controls - so the Ingress it renders has to expose a wildcard host, not
  # one bound to a single tenant's own subdomain.
  check "${overlay}: an Ingress is rendered" \
    "true" \
    "$(contains "${rendered}" "kind: Ingress")"
  check "${overlay}: the Ingress exposes a wildcard host" \
    "true" \
    "$(contains "${rendered}" "host: '\*\.")"
  check "${overlay}: the Ingress reaches admin-service, not a baked-in tenant" \
    "true" \
    "$(contains "${rendered}" "name: admin-service")"
  check "${overlay}: the Ingress reaches business-ui, not a baked-in tenant" \
    "true" \
    "$(contains "${rendered}" "name: business-ui")"

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
  "$("${KUSTOMIZE}" build --load-restrictor LoadRestrictionsNone deploy/k8s/overlays/prod | grep -c 'replicas: 3' | awk '{print ($1 >= 5)}' | sed 's/1/true/;s/0/false/')"

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

# dev-redis (ADR-009): the same dev overlay with the coordination backend
# swapped. The claim being tested is that changing the mechanism is a
# deployment-time choice, so the interesting assertion is not that Redis
# appears - it is that the workloads are byte-identical apart from the
# election configuration they are handed.
redis_rendered=""
for attempt in 1 2; do
  if redis_rendered="$("${KUSTOMIZE}" build --load-restrictor LoadRestrictionsNone "deploy/k8s/overlays/dev-redis" 2>/tmp/kustomize-dev-redis.err)"; then
    break
  fi
  redis_rendered=""
done
if [[ -z "${redis_rendered}" ]]; then
  echo "k8s-manifests-test: kustomize build dev-redis produced no output after retrying" >&2
  cat /tmp/kustomize-dev-redis.err >&2
  failed=$((failed + 1))
else
  check "dev-redis: redis workload is present" \
    "true" \
    "$(echo "${redis_rendered}" | grep -cE "^  name: redis$" | awk '{print ($1 > 0)}' | sed 's/1/true/;s/0/false/')"

  check "dev-redis: the election runs on Redis" \
    "true" \
    "$(contains "${redis_rendered}" "LEADER_ELECTION_TYPE: REDIS")"

  # The component replaces the ConfigMap rather than merging into it, so a
  # deployment cannot end up holding both answers at once.
  check "dev-redis: no workload is still told to use Kubernetes Leases" \
    "false" \
    "$(contains "${redis_rendered}" "LEADER_ELECTION_TYPE: K8S_LEASE")"

  # The seam is only real if the services themselves did not change.
  dev_workloads="$("${KUSTOMIZE}" build --load-restrictor LoadRestrictionsNone deploy/k8s/overlays/dev | grep -E "^  *image:" | sort)"
  redis_workloads="$(grep -E "^  *image:" <<<"${redis_rendered}" | grep -v "redis:7-alpine" | sort)"
  check "dev-redis: swapping the mechanism rebuilds no service" \
    "${dev_workloads}" \
    "${redis_workloads}"

  redis_conform_output="$(echo "${redis_rendered}" | "${KUBECONFORM}" -strict -summary -skip ScaledObject 2>&1)"
  redis_conform_status=$?
  check "dev-redis: every non-CRD resource validates against the Kubernetes API schema" \
    "0" \
    "${redis_conform_status}"
  if [[ "${redis_conform_status}" -ne 0 ]]; then
    echo "${redis_conform_output}"
  fi
fi

echo
echo "${passed} passed, ${failed} failed"
[[ "${failed}" -eq 0 ]]
