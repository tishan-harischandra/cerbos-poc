// Deterministic per-VU identity, shared by every scenario so the same VU
// always authenticates as the same load-model user for the run's duration
// (one password grant per VU, per §15).
import { usernameFor } from './config.js';

// __VU is unique across every active VU in the run, including across
// scenarios executing concurrently, so this never collides between the
// business, capability and token-baseline scenario pools.
export function usernameForVU() {
  return usernameFor(__VU);
}
