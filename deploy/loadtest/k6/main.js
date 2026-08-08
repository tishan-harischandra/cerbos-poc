// The §15.3 k6 load suite (issue #25): 1,000 concurrent virtual users,
// protocol-level only, covering the business endpoint, all three §12.7
// capability snapshot shapes, a token-endpoint baseline and a mid-run
// permission-mutation scenario, with the §15.3 objectives enforced as k6
// thresholds so a run returns a pass/fail verdict.
//
//   make loadtest                 - seeds the full population, then runs this
//   scripts/loadtest-run.sh       - runs this suite alone against a stack
//                                   already seeded (`PROFILE=load`)
//
// Every scenario, VU count, stage duration and target host is in
// lib/config.js, driven entirely by environment variables so this file
// never has to change between the demo-population smoke run the harness is
// exercised with and the full 1,000-VU run.
import { VUS, TOTAL_VUS, stagesFor } from './lib/config.js';
import { businessAuthz } from './scenarios/business_authz.js';
import { capabilityModule } from './scenarios/capability_module.js';
import { capabilityInstance } from './scenarios/capability_instance.js';
import { capabilityRow } from './scenarios/capability_row.js';
import { tokenBaseline } from './scenarios/token_baseline.js';
import { mutationConvergence } from './scenarios/mutation_convergence.js';

export { businessAuthz, capabilityModule, capabilityInstance, capabilityRow, tokenBaseline, mutationConvergence };

export const options = {
  scenarios: {
    business_authz: {
      executor: 'ramping-vus',
      exec: 'businessAuthz',
      startVUs: 0,
      stages: stagesFor(VUS.businessAuthz),
    },
    capability_module: {
      executor: 'ramping-vus',
      exec: 'capabilityModule',
      startVUs: 0,
      stages: stagesFor(VUS.capabilityModule),
    },
    capability_instance: {
      executor: 'ramping-vus',
      exec: 'capabilityInstance',
      startVUs: 0,
      stages: stagesFor(VUS.capabilityInstance),
    },
    capability_row: {
      executor: 'ramping-vus',
      exec: 'capabilityRow',
      startVUs: 0,
      stages: stagesFor(VUS.capabilityRow),
    },
    token_baseline: {
      executor: 'ramping-vus',
      exec: 'tokenBaseline',
      startVUs: 0,
      stages: stagesFor(VUS.tokenBaseline),
    },
    mutation_convergence: {
      executor: 'ramping-vus',
      exec: 'mutationConvergence',
      startVUs: 0,
      stages: stagesFor(VUS.mutationConvergence),
    },
  },
  thresholds: {
    // §15.3: warm decision latency, measured on the ADS decision endpoint
    // only (lib/metrics.js's warmDecisionLatency), never including a token
    // grant or refresh call.
    warm_decision_latency_ms: ['p(95)<15', 'p(99)<30'],
    // §15.3: 99% of committed permission updates visible within 5 seconds.
    permission_convergence_ms: ['p(99)<5000'],
    revocation_convergence_ms: ['p(99)<5000'],
    // §15.3: no business operation proceeds without an explicit allow.
    business_op_without_allow: ['count==0'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
};

// Logged once at the top of every run so `make loadtest`'s own console
// output states the target concurrency without needing the summary.
export function setup() {
  console.log(`loadtest: targeting ${TOTAL_VUS} concurrent virtual users across ${Object.keys(VUS).length} scenarios`);
}

// handleSummary prints the sizing outputs issue #25 asks for (achieved
// throughput, latency distribution and the objectives' pass/fail state)
// alongside k6's own summary. The full result set is written by the
// `--summary-export` flag scripts/loadtest-run.sh passes, into the versioned
// results directory that script builds - never a path this file hardcodes,
// so the same script works unchanged whether it runs against a live stack
// or under `k6 inspect`, which never calls handleSummary at all.
export function handleSummary(data) {
  return {
    stdout: JSON.stringify(summarize(data), null, 2),
  };
}

function summarize(data) {
  const metrics = data.metrics || {};
  const pick = (name, stat) => (metrics[name] && metrics[name].values ? metrics[name].values[stat] : undefined);
  return {
    warmDecisionLatencyMs: { p95: pick('warm_decision_latency_ms', 'p(95)'), p99: pick('warm_decision_latency_ms', 'p(99)') },
    permissionConvergenceMs: { p99: pick('permission_convergence_ms', 'p(99)') },
    revocationConvergenceMs: { p99: pick('revocation_convergence_ms', 'p(99)') },
    businessOpsWithoutAllow: pick('business_op_without_allow', 'count'),
  };
}
