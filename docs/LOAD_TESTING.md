# Load testing guide

Everything needed to reproduce a §15.3 run without asking the author: what the
host must provide, how to run it against either deployment path, and how to
read the result. Measurements taken so far are in
[`MEASURED_FINDINGS.md`](MEASURED_FINDINGS.md).

## Minimum host specification

`scripts/loadtest-preflight.sh` enforces this before anything is started, so a
run either begins valid or refuses clearly rather than failing an hour in:

| Resource | Minimum | Why |
|---|---|---|
| Available memory | 8 GiB (`MemAvailable`) | Keycloak with 600,000 users, its own PostgreSQL, the authorization database, Redpanda, the PDP and the k6 process |
| Free disk | 10 GiB | The two seeded databases, Kafka's log, and the results directory |
| CPU | 8 cores recommended | 1,000 VUs and the whole control plane share the host; fewer cores measures the load generator, not the platform |
| Network | none | Everything runs locally; no egress except the initial image pulls |

Both thresholds are overridable (`LOADTEST_MIN_FREE_MEM_KB`,
`LOADTEST_MIN_FREE_DISK_KB`), which is how the preflight's own test exercises
it. Lowering them to make a run start on a smaller host does not make the
numbers mean anything.

`k6` is not installed on the host: `scripts/k6.sh` runs it in a container, the
same way `scripts/go.sh` runs the Go toolchain.

## The load model (§15.1)

| Dimension | Full profile | Demo profile |
|---|---|---|
| Tenants | 5 | 1 |
| Hospitals | 20 (4 per tenant) | 1 |
| Canonical roles | 250 | a handful |
| Users | 600,000 | tens |
| Roles per user | 70 (42M mappings) | 1-2 |
| Users with overrides | ~5% (~150,000 rows) | a few |
| Concurrent VUs | 1,000, protocol-level | 10s |

The two profiles differ by configuration only, never by code path -
`PROFILE=demo` exists to exercise the harness, not to produce a smaller
measurement.

**There are no browser VUs.** §15.1's concurrency is protocol-level: each VU
takes one password grant and then rotates refresh tokens. A thousand headless
browsers is not achievable on the target hardware and would measure Chrome.

## Running it against docker compose

```bash
make loadtest                        # preflight, clean stack, full seed, full run
make loadtest LOADTEST_PROFILE=demo  # same harness, small population
```

`make loadtest` is the whole sequence:

1. `scripts/loadtest-preflight.sh` - refuse an under-resourced host.
2. `make up` - a clean, healthy stack.
3. `make loadtest-seed PROFILE=load` - bulk-load Keycloak (through
   `libs/keycloakbulkload`, writing to Keycloak's own database rather than
   through its Admin API, which would take days at this size) and the
   authorization database.
4. `scripts/loadtest-run.sh` - the k6 suite, results written to
   `dist/loadtest-results/<timestamp>-<git-sha>/`.

The seed is the slow part and it is idempotent, so a second run against an
already-seeded stack can skip straight to step 4:

```bash
bash scripts/loadtest-run.sh
```

## Running it against the `deploy/k8s` overlays

The suite talks to two URLs and nothing else, so it does not care how the
stack is deployed - only that it can reach the Administration Service and
Keycloak. Against a cluster, forward those two and point the suite at them:

```bash
kubectl -n cerbos-poc-dev port-forward svc/admin-service 4200:8081 &
kubectl -n cerbos-poc-dev port-forward svc/keycloak 8081:8080 &

LOADTEST_BASE_URL=http://127.0.0.1:4200/api/ads \
LOADTEST_ADMIN_SERVICE_URL=http://127.0.0.1:4200/api/admin \
LOADTEST_KEYCLOAK_URL=http://127.0.0.1:8081 \
  bash scripts/loadtest-run.sh
```

Two differences from the compose path, both worth planning for:

- **The seed has to run against the cluster's databases**, not compose's.
  Forward `svc/postgres` and run `scripts/loadtest-seed.sh` with
  `POSTGRES_HOST=127.0.0.1`, the way `scripts/k8s-walkthrough.sh` does for the
  demo seed.
- **A port-forward is a single-connection tunnel through the API server** and
  becomes the bottleneck long before the platform does. Any run whose numbers
  are meant to be compared with a compose run needs an Ingress or a
  `NodePort`, not `kubectl port-forward`. Neither is defined by this layout
  (see the README's remaining manual steps).

Because of that, the §15.3 numbers of record are taken against compose. The
Kubernetes run exists to show the same suite passes the same thresholds on a
real cluster, not to be compared millisecond for millisecond.

## What the run enforces

The §15.3 objectives are k6 `thresholds`, so the suite returns a verdict and a
breach exits non-zero. There is no interpretation step where someone decides
whether a number is acceptable:

| Threshold | Objective |
|---|---|
| `warm_decision_latency_ms: p(95)<15, p(99)<30` | Warm decision latency on `POST /internal/authz/check`, never including a token grant or refresh |
| `permission_convergence_ms: p(99)<5000` | A committed permission change visible within five seconds |
| `revocation_convergence_ms: p(99)<5000` | The same, measured separately for revocations (§10.3) |
| `business_op_without_allow: count==0` | No business operation proceeded without an explicit allow. A hard failure at any count |

Six scenarios run concurrently: `business_authz`, `capability_module`,
`capability_instance`, `capability_row`, `token_baseline` and
`mutation_convergence`. The token baseline is separate on purpose - if
Keycloak's token endpoint saturates first, that is a finding about the
identity provider, and it should be attributable rather than confused with
decision latency.

**These threshold values are §15.3's suggested targets, not measurements**
(issue #25, marked HITL). The first real run on real hardware is expected to
force a renegotiation; that conversation is the point.

## Reading a result

Each run writes `dist/loadtest-results/<timestamp>-<git-sha>/`:

| File | What it holds |
|---|---|
| `config.json` | Every `LOADTEST_*` variable that applied, plus the git SHA and timestamp - what produced these numbers |
| `summary.json` | k6's own summary: every metric, every threshold and whether it passed |
| `stdout.log` | The console output, including the concurrency banner |

Read them in this order:

1. **Did every threshold pass?** `jq '.metrics | to_entries[] | select(.value.thresholds)' summary.json`.
   A failure here is the answer; everything else is diagnosis.
2. **Was the load actually applied?** Check the VU count and the iteration
   count. A run that never reached 1,000 VUs measured the ramp, not the
   platform.
3. **Where did time go?** Compare `warm_decision_latency_ms` against
   `http_req_duration` for the token scenario. If the token endpoint is slow
   and decisions are not, the identity provider is the limit.
4. **What did the caches do?** `ads_cache_hits_total` and
   `ads_cache_misses_total` on the ADS `/metrics` endpoint, per cache. A low
   `user_overrides` hit ratio at 600,000 users is an expected, informative
   result, not a number to tune until it looks good.

Numbers are only comparable against another run with the same `config.json`
and the same host. Record both alongside the result, as
[`MEASURED_FINDINGS.md`](MEASURED_FINDINGS.md) does.
