// Scenario: business endpoint authorization (§12.7's "Backend operation"
// row) - the POST /internal/authz/check path every write and read API calls
// on every request, exercised here exactly the way apps/ads/internal/authz
// serves it.
import http from 'k6/http';
import { check, sleep } from 'k6';
import { BASE_URL } from '../lib/config.js';
import { ensureFreshToken, authHeader } from '../lib/auth.js';
import { warmDecisionLatency, businessOpWithoutAllow, businessAuthzRequests } from '../lib/metrics.js';
import { identityForVU } from '../lib/identity.js';

// Each VU is its own JS VM instance in k6, so this module-level state is
// already per-VU - no VU-indexed map is needed.
let tokenSet = null;

export function businessAuthz() {
  const identity = identityForVU();
  tokenSet = ensureFreshToken(identity, tokenSet);
  if (!tokenSet) {
    sleep(1);
    return;
  }

  const payload = JSON.stringify({
    resources: [
      {
        kind: 'patient_record',
        id: `loadtest-patient-${__VU}`,
        attributes: { tenantId: identity.tenantId, hospitalId: identity.hospitalId, status: 'ACTIVE' },
        actions: ['read'],
      },
    ],
  });

  const res = http.post(`${BASE_URL}/internal/authz/check`, payload, {
    headers: { 'Content-Type': 'application/json', ...authHeader(tokenSet) },
    tags: { name: 'business_authz_check' },
  });
  businessAuthzRequests.add(1);

  const ok = check(res, { 'business authz check answered 200': (r) => r.status === 200 });
  if (ok) {
    warmDecisionLatency.add(res.timings.duration);
    const allowed = res.json('resources.0.actions.read.allowed');
    // §15.3 fail-closed, enforced as code rather than only asserted: the
    // simulated business read only ever "proceeds" inside this branch, so a
    // proceed without an explicit allow is structurally impossible unless
    // this gate itself is broken - the case businessOpWithoutAllow exists to
    // catch.
    if (allowed === true) {
      // The business read proceeds here in a real caller.
    } else if (allowed !== false) {
      businessOpWithoutAllow.add(1);
    }
  }

  sleep(1);
}
