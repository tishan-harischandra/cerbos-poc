// Custom metrics the §15.3 thresholds are enforced against, plus the
// §17.1 attribution metrics (issue #25 acceptance criterion: "observed
// bottlenecks are identified by component using the exported metrics").
import { Trend, Counter } from 'k6/metrics';

// Warm decision latency: the ADS's own decision endpoint only, never
// including a token grant/refresh call, so this measures exactly what
// §15.1's hot path measures (backend -> ADS cache -> Cerbos gRPC -> backend).
export const warmDecisionLatency = new Trend('warm_decision_latency_ms', true);

// Permission convergence: time from a mid-run mutation's write commit to the
// first authz decision that reflects it, on a healthy ADS replica.
export const permissionConvergenceLatency = new Trend('permission_convergence_ms', true);

// Revocation is a mutation too (a permission going from enabled to
// disabled); tracked separately because §15.3 names both and a suite that
// only ever tests grants could hide a revocation-specific bug.
export const revocationConvergenceLatency = new Trend('revocation_convergence_ms', true);

// §15.3 "fail closed": incremented only if a scenario's own code proceeds to
// simulate a business operation without having received an explicit allow
// first. Every scenario below gates its simulated write behind the decision
// it just received, so a non-zero count here is a defect in this harness or
// the system under test, never a benign outcome.
export const businessOpWithoutAllow = new Counter('business_op_without_allow');

// Per-component attribution (§15.2, §17.1): the ADS reports its own cache
// hit ratio and Cerbos engine time on /metrics (apps/ads/internal/adsmetrics),
// scraped by Prometheus during the run rather than measured here - these
// counters instead attribute where *this suite* spent time, so a scrape-side
// dashboard and this suite's own summary agree on which endpoint was asked.
export const businessAuthzRequests = new Counter('scenario_business_authz_requests');
export const capabilityModuleRequests = new Counter('scenario_capability_module_requests');
export const capabilityInstanceRequests = new Counter('scenario_capability_instance_requests');
export const capabilityRowRequests = new Counter('scenario_capability_row_requests');
export const tokenBaselineRequests = new Counter('scenario_token_baseline_requests');
