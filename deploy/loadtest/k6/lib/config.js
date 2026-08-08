// Environment-derived configuration for the §15.3 k6 load suite (issue #25).
// Every value has a load-profile-shaped default so `make loadtest` needs no
// extra flags, but every value is overridable so the harness itself can be
// exercised against the small demo profile (scripts/loadtest-seed.sh demo).

// Where the business endpoints live. The Admin Console's nginx proxies both
// the ADS (`/api/ads/...`) and the Administration Service (`/api/admin/...`)
// exactly like a real browser session (apps/admin-console/nginx.conf.template),
// so the load suite never talks to either backend directly.
export const BASE_URL = __ENV.LOADTEST_BASE_URL || 'http://ads:8080';
export const ADMIN_SERVICE_URL = __ENV.LOADTEST_ADMIN_SERVICE_URL || 'http://admin-service:8081';

// The load-test identity provider (issue #24), not the demo Keycloak: the
// full population only exists there.
export const KEYCLOAK_URL = __ENV.LOADTEST_KEYCLOAK_URL || 'http://keycloak-loadtest:8080';
export const REALM = __ENV.LOADTEST_REALM || 'cerbos-poc-loadtest';
export const CLIENT_ID = __ENV.LOADTEST_CLIENT_ID || 'patient-app';
export const PASSWORD = __ENV.LOADTEST_PASSWORD || 'Load-Test-Only-P@ss1';

// libs/loadmodel.FullLoadConfig: 600,000 users named load-user-0000000
// through load-user-0599999 (libs/loadmodel.loadmodel.go's Username format).
export const TOTAL_LOAD_USERS = Number(__ENV.LOADTEST_TOTAL_USERS || 600000);
export const USERNAME_PREFIX = __ENV.LOADTEST_USERNAME_PREFIX || 'load-user-';
export const USERNAME_DIGITS = Number(__ENV.LOADTEST_USERNAME_DIGITS || 7);

export const TENANT_ID = __ENV.LOADTEST_TENANT_ID || 'tenant-0';
export const HOSPITAL_ID = __ENV.LOADTEST_HOSPITAL_ID || 'hospital-0-0';

// The role matrix row the mutation-convergence scenario flips mid-run
// (§10, §15.3's "permission convergence" objective).
export const MUTATION_ROLE = __ENV.LOADTEST_MUTATION_ROLE || 'canonical-role-0000';
export const MUTATION_RESOURCE = __ENV.LOADTEST_MUTATION_RESOURCE || 'patient_record';
export const MUTATION_ACTION = __ENV.LOADTEST_MUTATION_ACTION || 'read';

// §15.3: "sustain 1,000 concurrent virtual users". Split across the scenario
// mix (§12.7's three snapshot shapes, the business endpoint, the token
// baseline and the mutation scenario) so the *sum* sustains the target
// rather than each scenario individually chasing it.
export const VUS = {
  businessAuthz: Number(__ENV.LOADTEST_VUS_BUSINESS || 600),
  capabilityModule: Number(__ENV.LOADTEST_VUS_MODULE || 100),
  capabilityInstance: Number(__ENV.LOADTEST_VUS_INSTANCE || 100),
  capabilityRow: Number(__ENV.LOADTEST_VUS_ROW || 100),
  tokenBaseline: Number(__ENV.LOADTEST_VUS_TOKEN || 70),
  mutationConvergence: Number(__ENV.LOADTEST_VUS_MUTATION || 30),
};

export const TOTAL_VUS = Object.values(VUS).reduce((sum, n) => sum + n, 0);

// Ramp-up, steady state and ramp-down (§15: "defined ramp-up, steady state
// and ramp-down").
export const RAMP_UP = __ENV.LOADTEST_RAMP_UP || '2m';
export const STEADY_STATE = __ENV.LOADTEST_STEADY_STATE || '5m';
export const RAMP_DOWN = __ENV.LOADTEST_RAMP_DOWN || '1m';

// Access token lifespan tuned so refresh-token rotation is a small fraction
// of request volume (§15: "identity provider is not the bottleneck"). A VU
// refreshes once its token is within this many seconds of the realm's
// configured access-token lifespan; the realm itself controls the real
// expiry, this only bounds how long one VU keeps reusing a token before
// asking again.
export const TOKEN_REUSE_SECONDS = Number(__ENV.LOADTEST_TOKEN_REUSE_SECONDS || 240);

// stagesFor builds the ramping-vus executor stages one scenario needs to
// reach `target` and hold it for the steady state, shared so every scenario
// ramps and drains on the same schedule.
export function stagesFor(target) {
  return [
    { duration: RAMP_UP, target },
    { duration: STEADY_STATE, target },
    { duration: RAMP_DOWN, target: 0 },
  ];
}

// usernameFor derives one deterministic load-model username from a
// scenario-scoped counter, matching libs/loadmodel's `load-user-%07d`
// format so every request authenticates as a user the seed actually wrote.
export function usernameFor(counter) {
  const index = counter % TOTAL_LOAD_USERS;
  return USERNAME_PREFIX + String(index).padStart(USERNAME_DIGITS, '0');
}
