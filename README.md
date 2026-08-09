# Cerbos multi-tenant authorization prototype

A working prototype of the design in
[`docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md`](docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md).
The product requirements live in
[`docs/PRD_Cerbos_Authorization_Prototype.md`](docs/PRD_Cerbos_Authorization_Prototype.md).

The workspace is an Nx monorepo driving Go and Angular in one dependency graph,
running as a `docker compose` stack with PostgreSQL and the Cerbos PDP.

The ADS serves real authorization decisions on `POST /internal/authz/check`,
decided by a hand-authored `patient_record` policy through the single synthetic
evaluation role. Permission assignments are seeded in process; the authorization
database arrives with the role matrix slice.

## Prerequisites

Only two things must exist on the host:

| Tool | Version | Why |
| --- | --- | --- |
| Node.js | 24.x (npm 11) | Runs Nx and the Angular toolchain |
| Docker or Podman | with `docker compose` | Runs every other toolchain |
| Python 3 with PyYAML | 3.10+ | Runs the compose contract test |

**No Go, no JVM, no Angular CLI on the host.** The Go toolchain runs inside a
container via `scripts/go.sh`, and `scripts/bin/go` is a shim on `PATH` (the
`Makefile` adds it) so tools that insist on a `go` binary — notably the Nx Go
plugin — work without a host install. Liquibase will follow the same rule in
later slices.

Podman users: `docker compose` needs a socket, so run
`systemctl --user start podman.socket` and export
`DOCKER_HOST=unix:///run/user/$(id -u)/podman/podman.sock`.

## Quick start

```bash
cp .env.example .env
make up      # build the images and wait until every service is healthy
make smoke   # verify the running stack end to end
make down    # stop the stack, keep the database volume
make clean   # remove containers, volumes, local images and build caches
```

## Exposed URLs

Only the Administration Service is published to the host. It serves the Admin
Console and proxies the console's API calls (ADR-008), so everything else -
the PDP, the ADS, the database - is reachable only from inside the compose
network (§16.1).

| Service | Host URL | In-network address | Published? |
| --- | --- | --- | --- |
| Admin Console | <http://localhost:4200> | `admin-service:8081` | yes (`ADMIN_CONSOLE_PORT`) |
| Administration API | <http://localhost:4200/api/admin/...> | `admin-service:8081/admin/...` | same port as the console |
| ADS health | <http://localhost:4200/api/ads/healthz> | `ads:8080/healthz` | proxied by admin-service only |
| ADS readiness | <http://localhost:4200/api/ads/readyz> | `ads:8080/readyz` | proxied by admin-service only |
| ADS decisions | `POST /api/ads/internal/authz/check` | `ads:8080/internal/authz/check` | proxied by admin-service only |
| Cerbos PDP (gRPC) | not reachable | `cerbos:3593` | no |
| Cerbos PDP (HTTP) | not reachable | `cerbos:3592` | no |
| PostgreSQL | not reachable | `postgres:5432` | no |

The Admin Console landing page renders a live badge sourced from the ADS health
endpoint: green `healthy` when the ADS answers, red `unreachable` otherwise.

## Demo credential placeholders

`.env.example` holds local demo placeholders only. Nothing in it is a real
credential, and `.env` is git-ignored.

| Variable | Placeholder | Used by |
| --- | --- | --- |
| `COMPOSE_PROJECT_NAME` | `cerbos-poc` | compose project namespacing |
| `ADMIN_CONSOLE_PORT` | `4200` | host port for the Admin Console |
| `POSTGRES_USER` | `cerbos_poc` | PostgreSQL superuser |
| `POSTGRES_PASSWORD` | `change-me` | PostgreSQL password |
| `POSTGRES_DB` | `cerbos_poc` | PostgreSQL database name |

Keycloak, Gitea and the Cerbos Admin API credentials arrive with the slices that
introduce those services.

## Vendor telemetry

Every vendor product in the stack that ships an opt-out is opted out, so
nothing here phones home on a contributor's or CI's behalf:

| Product | Setting | Where |
| --- | --- | --- |
| Nx | `analytics: false`, `neverConnectToCloud: true` | `nx.json` |
| Nx (defense-in-depth) | `NX_NO_CLOUD=true` | CI env |
| Angular CLI | `NG_CLI_ANALYTICS=false` | CI env |
| Cerbos | `telemetry.disabled: true` | `deploy/cerbos/config.yaml` |
| Redpanda | `--set=redpanda.enable_usage_stats=false` | `docker-compose.yml` |

Go's own telemetry (`go telemetry`) is opt-in and off by default, so it needs
no override here.

## Workspace layout

```
apps/ads              Go service: health, readiness and the decision endpoint
apps/admin-console    Angular app: live platform status (built into the
                      admin-service image, which serves it - ADR-008)
libs/permissioncontext  Assembles permissionContext data; never a verdict
libs/cerbosclient     Long-lived gRPC channel to the PDP
deploy/cerbos         Cerbos config, the served policy bundle and the ADR-003 control
deploy/k8s            kustomize layout for a real cluster (see "Kubernetes deployment")
tests/architecture    Executable checks for constraints no compiler enforces
scripts/              Container-backed toolchain helpers and infrastructure tests
```

## Development

```bash
make test          # every project's tests, the policy suite, the compose contract
make policy-test   # compile the policies and run the precedence matrix alone
make smoke         # health checks and real decisions against a running stack
make graph         # the single Go + Angular dependency graph
make gen           # every project's code generators
npx nx affected --target=test --base=main
```

The Nx daemon is disabled (`useDaemonProcess: false` in `nx.json`). On file
systems where its watcher misses changes, the daemon serves a stale file map and
Nx replays a cached result for a target whose test files have changed — a new
failing test is silently never run. Correct caching matters more here than the
daemon's speed-up.

The Nx graph spans both languages, but the projects stay independent: a change
under `apps/admin-console` never marks the Go service as affected.
`scripts/tests/nx-affected-isolation.sh` asserts exactly that, and CI runs it.

## Load testing

`make loadtest` runs the §15.3 k6 suite (`deploy/loadtest/k6`) at 1,000
concurrent virtual users against a freshly seeded, full-scale (600,000-user)
population: `scripts/loadtest-preflight.sh` refuses on an under-resourced
host, `make up` brings the stack to a clean state, `loadtest-seed` bulk-loads
the population, and `scripts/loadtest-run.sh` runs the suite and writes
results to `dist/loadtest-results/<timestamp>-<git-sha>/`. The §15.3
objectives (warm decision latency, permission convergence, fail-closed) are
k6 `thresholds`, so a breach exits non-zero.

```bash
make loadtest                       # the full 1,000-VU run
make loadtest LOADTEST_PROFILE=demo # exercise the harness against the small demo population
```

**This suite is marked HITL** (issue #25): the threshold values encode
§15.3's suggested targets, but they will likely need renegotiating against
the first real measurements taken on real hardware.

## Kubernetes deployment

`deploy/k8s` is a `kustomize` layout for running the same core walking-skeleton
services docker-compose brings up by default — `postgres`, `cerbos`,
`keycloak`, `redpanda`, `ads`, `admin-service` (which serves the Admin
Console), `resource-service` and `business-ui` — on a real cluster instead. The `oracle`,
`loadtest`, `policy-release` and `observability` compose profiles are out of
scope for this layout (see issue #55).

```
deploy/k8s/base/<service>/   One Deployment or StatefulSet, Service, and
                             (for scalable services) a KEDA ScaledObject
                             per directory.
deploy/k8s/base/common/      Shared IdP config/credential and Postgres DSN
                             every Go service consumes.
deploy/k8s/overlays/dev/     kind/minikube: 1 replica per service, `:dev`
                             image tags.
deploy/k8s/overlays/prod/    A real cluster: 3+ replica floors, pinned
                             image tags (edit REGISTRY/TAG first).
```

Cerbos's committed policy tree and the ADS/admin-service capability and
authorization catalogs are baked into a small `cerbos-poc/cerbos-assets`
image (`deploy/cerbos/Dockerfile`) and copied into an `emptyDir` by an
initContainer on every pod that needs one, standing in for compose's host
bind mounts — the combined tree is well over the ~1MiB a ConfigMap allows,
and its per-resource directory structure (ADR-006) doesn't survive a flat
ConfigMap's key space anyway.

To run locally against `kind` or `minikube`:

```bash
docker build -f deploy/cerbos/Dockerfile -t cerbos-poc/cerbos-assets:dev .
docker build -f apps/ads/Dockerfile -t cerbos-poc/ads:dev .
docker build -f apps/admin-service/Dockerfile -t cerbos-poc/admin-service:dev .
docker build -f apps/resource-service/Dockerfile -t cerbos-poc/resource-service:dev .
docker build -f apps/business-ui/Dockerfile -t cerbos-poc/business-ui:dev .
kind load docker-image cerbos-poc/cerbos-assets:dev cerbos-poc/ads:dev \
  cerbos-poc/admin-service:dev cerbos-poc/resource-service:dev \
  cerbos-poc/business-ui:dev
kubectl apply -k deploy/k8s/overlays/dev
```

`make k8s-validate` (wired into `make ci`) renders both overlays with
`kustomize` and validates every resource against the Kubernetes API schema
(KEDA's `ScaledObject` CRD is skipped — no cluster is required, everything
runs through Docker via `scripts/kustomize.sh`/`scripts/kubeconform.sh`, the
same pattern as `scripts/go.sh`/`scripts/k6.sh`).

**Remaining manual steps before a real cluster deploy:** a KEDA operator
installed cluster-wide; the `prod` overlay's `REGISTRY`/`TAG` image
placeholders replaced (or set with `kustomize edit set image`); an Ingress or
LoadBalancer Service in front of `admin-service`/`business-ui` (compose's
host port publishing has no direct equivalent here and isn't defined by this
layout); and the dev-only fixture credentials in `deploy/k8s/base/common` and
`deploy/k8s/base/postgres` replaced with real secrets from a cluster secret
manager.

## Leader election

Two workloads must have exactly one active instance: the policy release
pipeline (`policy-controller`) and the outbox publisher (inside
`admin-service`). Which mechanism decides that is an operational choice, so
services depend only on the `libs/leaderlock` port and an operator selects an
adapter with `LEADER_ELECTION_TYPE` (ADR-009). No service is rebuilt to change
it, and an architecture test fails the build if one names an adapter directly.

**There is deliberately no default.** An unset `LEADER_ELECTION_TYPE` refuses
to start. Every other setting here has a sensible fallback; this one cannot,
because the only "safe" guess — coordinate with nothing — silently elects every
replica, which is exactly the failure the port exists to prevent.

| `LEADER_ELECTION_TYPE` | Mechanism | Guarantee | Choose it when |
|---|---|---|---|
| `PG_ADVISORY` | `pg_try_advisory_lock` on a dedicated session | **Mutual exclusion.** No ttl, no renewal, no split-brain window | You run PostgreSQL and want the strongest guarantee available here. The compose default |
| `DATABASE` | A `leader_lock` lease row, expiring on the database clock | Lease: split-brain window | You run Oracle, or want no second dependency. The only database-backed type that runs on both engines |
| `K8S_LEASE` | A `coordination.k8s.io/v1` Lease | Lease: split-brain window | You run on Kubernetes. The `deploy/k8s` default: the control plane already keeps it available, and `kubectl get lease` shows who leads |
| `REDIS` | `SET NX PX` with compare-and-extend renewal | Lease, and weaker across a Redis failover | You already run Redis and would rather keep election traffic off the database |
| `SINGLE` | Always leader, no coordination | **None** | One replica, or a test. Selecting it with more than one replica means every replica does the singleton work |

**A lease is not mutual exclusion.** Four of the five adapters can let a paused
or partitioned leader overlap briefly with its successor. Both current
consumers are safe under that: outbox delivery is already at-least-once with an
idempotent consumer, and the release pipeline installs atomically. A future
consumer that needs true exclusion cannot get it by switching adapters, because
the port only ever promises its weakest member. There are no fencing tokens —
ADR-009 records why an epoch nothing downstream can validate is worse than
saying so plainly.

One shared contract suite (`libs/leaderlock/leaderlockcontract`) defines what an
election means, and every adapter passes it, so the "pick a backend" promise
rests on the backends genuinely agreeing:

```bash
scripts/tests/leaderlock-contract.sh dual   # DATABASE on PostgreSQL and Oracle
scripts/tests/leaderlock-contract.sh redis  # needs the redis compose profile
```

`SINGLE` and `K8S_LEASE` need no infrastructure — `K8S_LEASE` runs its whole
contract against a fake API server — so `go test ./libs/leaderlock/...` already
covers them offline.

Redis is optional the way Oracle is: a `redis` compose profile, and a
`deploy/k8s/components/redis` kustomize component. The `dev-redis` overlay
applies it, and the manifest test asserts that swapping the mechanism leaves
every service image untouched.

## Architectural constraints

Permission precedence — mandatory deny beats user revoke beats user grant beats
role grant beats default deny — lives **exclusively** in Cerbos policy, under
the single synthetic role `sys:permission-evaluator`. The ADS assembles
`permissionContext` data and never computes a verdict. Any Go code that orders
deny above grant is a defect.

Three layers hold that line:

- **The policy suite** (`make policy-test`) states the rules and proves all seven
  §19.1 cases for `read`, `update` and `delete`, plus tenant and hospital
  isolation. `deploy/cerbos/control/` holds a deliberate counter-example showing
  an allow on one role defeating a deny on another — the hazard the synthetic
  role exists to remove. It is compiled and tested but kept out of the bundle the
  PDP serves, since it is a proof, not a deployment.
- **The architecture test** (`tests/architecture`) parses every Go file and fails
  on any read of `roleGrantedActions`, `userGrantedActions` or
  `userRevokedActions` outside the package that defines them, because that is
  where a Go-side precedence implementation would begin. It also refuses to let a
  non-production policy reappear in the served bundle.
- **The end-to-end decision test** (`scripts/tests/decision-e2e.sh`, part of
  `make smoke`) drives the matrix through the running ADS into a real PDP, so a
  policy that never loaded or a context that never arrived cannot pass.
