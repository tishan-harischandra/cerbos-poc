// Scenario: mid-run permission mutation and convergence/revocation latency
// (§15.3: "99% of committed permission updates visible on healthy ADS
// replicas within 5 seconds", measured under load rather than only at rest).
//
// Each iteration flips one role permission's `enabled` flag through the
// Administration Service, then polls the ADS decision endpoint - as the
// probe user libs/loadmodel deterministically assigns that role to - until
// the decision reflects the flip, recording the time between the two.
import http from 'k6/http';
import { check, sleep } from 'k6';
import {
  ADMIN_SERVICE_URL,
  BASE_URL,
  MUTATION_ROLE,
  MUTATION_RESOURCE,
  MUTATION_ACTION,
} from '../lib/config.js';
import { ensureFreshToken, authHeader } from '../lib/auth.js';
import { permissionConvergenceLatency, revocationConvergenceLatency } from '../lib/metrics.js';
import { identityFor } from '../lib/identity.js';

// libs/loadmodel assigns canonical role index 0 - MUTATION_ROLE's default -
// to user index 0 by construction (role step 7, j=0 selects role index 0),
// so this probe user is deterministic without a directory lookup.
const PROBE = identityFor(0);
const TENANT_ID = PROBE.tenantId;
const HOSPITAL_ID = PROBE.hospitalId;
const POLL_TIMEOUT_MS = 10000;
const POLL_INTERVAL_MS = 200;

let adminTokenSet = null;
let probeTokenSet = null;

export function mutationConvergence() {
  adminTokenSet = ensureFreshToken(PROBE, adminTokenSet);
  probeTokenSet = ensureFreshToken(PROBE, probeTokenSet);
  if (!adminTokenSet || !probeTokenSet) {
    sleep(2);
    return;
  }

  const readRes = http.get(
    `${ADMIN_SERVICE_URL}/admin/authz/tenants/${TENANT_ID}/roles/${MUTATION_ROLE}/permissions`,
    { headers: authHeader(adminTokenSet), tags: { name: 'mutation_read_permissions' } }
  );
  if (!check(readRes, { 'reading the role matrix answered 200': (r) => r.status === 200 })) {
    sleep(2);
    return;
  }

  const body = readRes.json();
  const revision = body.revision;
  const permissions = body.permissions || [];
  const existing = permissions.find(
    (p) => p.resourceKey === MUTATION_RESOURCE && p.actionKey === MUTATION_ACTION
  );
  const wasEnabled = existing ? existing.enabled : false;
  const nextEnabled = !wasEnabled;

  const writeRes = http.put(
    `${ADMIN_SERVICE_URL}/admin/authz/tenants/${TENANT_ID}/roles/${MUTATION_ROLE}/permissions`,
    JSON.stringify({
      expectedRevision: revision,
      permissions: [
        {
          resourceKey: MUTATION_RESOURCE,
          actionKey: MUTATION_ACTION,
          enabled: nextEnabled,
          validFrom: new Date().toISOString(),
        },
      ],
    }),
    {
      headers: { 'Content-Type': 'application/json', ...authHeader(adminTokenSet) },
      tags: { name: 'mutation_write_permissions' },
    }
  );
  if (!check(writeRes, { 'writing the mutation answered 200': (r) => r.status === 200 })) {
    sleep(2);
    return;
  }

  const mutatedAt = Date.now();
  const converged = pollForDecision(nextEnabled);
  if (converged) {
    const latencyMs = Date.now() - mutatedAt;
    if (nextEnabled) {
      permissionConvergenceLatency.add(latencyMs);
    } else {
      revocationConvergenceLatency.add(latencyMs);
    }
  }
  check(converged, { 'the decision converged within the poll window': (c) => c === true });

  sleep(2);
}

function pollForDecision(expectAllowed) {
  const deadline = Date.now() + POLL_TIMEOUT_MS;
  while (Date.now() < deadline) {
    const res = http.post(
      `${BASE_URL}/internal/authz/check`,
      JSON.stringify({
        resources: [
          {
            kind: MUTATION_RESOURCE,
            id: 'loadtest-convergence-probe',
            attributes: { tenantId: TENANT_ID, hospitalId: HOSPITAL_ID, status: 'ACTIVE' },
            actions: [MUTATION_ACTION],
          },
        ],
      }),
      {
        headers: { 'Content-Type': 'application/json', ...authHeader(probeTokenSet) },
        tags: { name: 'mutation_poll_decision' },
      }
    );
    if (res.status === 200) {
      const allowed = res.json(`resources.0.actions.${MUTATION_ACTION}.allowed`);
      if (allowed === expectAllowed) {
        return true;
      }
    }
    sleep(POLL_INTERVAL_MS / 1000);
  }
  return false;
}
