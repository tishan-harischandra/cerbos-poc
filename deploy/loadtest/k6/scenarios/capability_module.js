// Scenario: module/collection composite capability snapshot (§12.7's
// "Module/collection composite" row) - loaded at login, tenant switch or
// hospital switch, never per navigation.
import http from 'k6/http';
import { check, sleep } from 'k6';
import { BASE_URL } from '../lib/config.js';
import { ensureFreshToken, authHeader } from '../lib/auth.js';
import { capabilityModuleRequests } from '../lib/metrics.js';
import { usernameForVU } from '../lib/identity.js';

let tokenSet = null;

export function capabilityModule() {
  const username = usernameForVU();
  tokenSet = ensureFreshToken(username, tokenSet);
  if (!tokenSet) {
    sleep(1);
    return;
  }

  const payload = JSON.stringify({
    module: 'financial',
    capabilityKeys: ['account.route.list'],
    context: {},
  });

  const res = http.post(`${BASE_URL}/internal/capabilities/evaluate`, payload, {
    headers: { 'Content-Type': 'application/json', ...authHeader(tokenSet) },
    tags: { name: 'capability_module_snapshot' },
  });
  capabilityModuleRequests.add(1);

  check(res, { 'module snapshot answered 200': (r) => r.status === 200 });
  sleep(2);
}
