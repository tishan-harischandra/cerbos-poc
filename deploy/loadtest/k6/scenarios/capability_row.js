// Scenario: row-level composite capability snapshot (§12.7's "Row-level
// composite" row) - one call renders a row's whole menu (read plus every
// workflow action) instead of N browser calls, so this scenario requests
// every row-menu capability for one instance in a single batch.
import http from 'k6/http';
import { check, sleep } from 'k6';
import { BASE_URL } from '../lib/config.js';
import { ensureFreshToken, authHeader } from '../lib/auth.js';
import { capabilityRowRequests } from '../lib/metrics.js';
import { usernameForVU } from '../lib/identity.js';

let tokenSet = null;

export function capabilityRow() {
  const username = usernameForVU();
  tokenSet = ensureFreshToken(username, tokenSet);
  if (!tokenSet) {
    sleep(1);
    return;
  }

  const payload = JSON.stringify({
    module: 'financial',
    capabilityKeys: ['account.action.delete', 'account.button.assign'],
    context: { accountId: `loadtest-account-${__VU}` },
  });

  const res = http.post(`${BASE_URL}/internal/capabilities/evaluate`, payload, {
    headers: { 'Content-Type': 'application/json', ...authHeader(tokenSet) },
    tags: { name: 'capability_row_snapshot' },
  });
  capabilityRowRequests.add(1);

  check(res, { 'row snapshot answered 200': (r) => r.status === 200 });
  sleep(2);
}
