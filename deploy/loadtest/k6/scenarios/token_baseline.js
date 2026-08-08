// Scenario: token endpoint baseline (§15: "a separate scenario that
// benchmarks the token endpoint alone, so identity cost is attributable and
// separable from authorization cost"). Every iteration is a fresh password
// grant against a distinct load-model user - deliberately never a refresh -
// so this scenario's own latency is exactly Keycloak's password-grant cost,
// uncontaminated by anything else this suite measures.
import { passwordGrant } from '../lib/auth.js';
import { tokenBaselineRequests } from '../lib/metrics.js';
import { usernameFor } from '../lib/config.js';
import { sleep } from 'k6';

let counter = 0;

export function tokenBaseline() {
  // A distinct user per iteration (not just per VU) so the baseline never
  // degenerates into measuring one cached session.
  const username = usernameFor(__VU * 1000000 + counter);
  counter += 1;

  passwordGrant(username);
  tokenBaselineRequests.add(1);

  sleep(1);
}
