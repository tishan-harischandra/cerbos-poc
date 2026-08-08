// Scenario: instance composite capability snapshot (§12.7's "Instance
// composite" row) - fetched once per page-resource load, reused across
// child routes and tabs.
import http from 'k6/http';
import { check, sleep } from 'k6';
import { BASE_URL } from '../lib/config.js';
import { ensureFreshToken, authHeader } from '../lib/auth.js';
import { capabilityInstanceRequests } from '../lib/metrics.js';
import { usernameForVU } from '../lib/identity.js';

let tokenSet = null;

export function capabilityInstance() {
  const username = usernameForVU();
  tokenSet = ensureFreshToken(username, tokenSet);
  if (!tokenSet) {
    sleep(1);
    return;
  }

  const payload = JSON.stringify({
    module: 'financial',
    capabilityKeys: ['account.route.edit'],
    context: { accountId: `loadtest-account-${__VU}` },
  });

  const res = http.post(`${BASE_URL}/internal/capabilities/evaluate`, payload, {
    headers: { 'Content-Type': 'application/json', ...authHeader(tokenSet) },
    tags: { name: 'capability_instance_snapshot' },
  });
  capabilityInstanceRequests.add(1);

  check(res, { 'instance snapshot answered 200': (r) => r.status === 200 });
  sleep(3);
}
