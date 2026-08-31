# Cerbos multi-tenant authorization prototype

A working prototype of the design in
[`docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md`](docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md).
The product requirements live in
[`docs/PRD_Cerbos_Authorization_Prototype.md`](docs/PRD_Cerbos_Authorization_Prototype.md).

The workspace is an Nx monorepo driving Go and Angular in one dependency graph.
It runs as a `docker compose` stack for a local demo, and from `deploy/k8s` on
a real cluster.

Role and user assignments live in the authorization database and reach the
decision path as *data*; the Cerbos policy tree - one file per resource across
the FHIR catalog - holds the rules, and permission precedence lives there and
nowhere else, behind the single synthetic role `sys:permission-evaluator`.
Changing who can do what never rebuilds or releases a policy, which is the
claim [the guided walkthrough](#the-guided-walkthrough) demonstrates.

## Documentation

| Document | What it answers |
| --- | --- |
| [`docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md`](docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md) | The design this prototype implements |
| [`docs/PRD_Cerbos_Authorization_Prototype.md`](docs/PRD_Cerbos_Authorization_Prototype.md) | What was in and out of scope, and why |
| [`docs/DESIGN_COVERAGE.md`](docs/DESIGN_COVERAGE.md) | Every design section §5-§19: implemented and cited, or out of scope with a reason |
| [`docs/DEVIATIONS.md`](docs/DEVIATIONS.md) | Every deviation from design v1.3, with its reason |
| [`docs/MEASURED_FINDINGS.md`](docs/MEASURED_FINDINGS.md) | Measured numbers and the configuration that produced them |
| [`docs/LOAD_TESTING.md`](docs/LOAD_TESTING.md) | Minimum host specification, how to run a load test, how to read the result |
| [`docs/adr/`](docs/adr/) | Decisions taken during implementation (ADR-008 onward; ADR-001-007 are in the design's §22) |

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

## The guided walkthrough

The claim this prototype exists to demonstrate: **a permission change is data,
not a policy release.** An administrator grants a role a permission, and a
clinician's browser reflects it within seconds - with nothing rebuilt, no
policy compiled and no service restarted anywhere in that path.

Start from a clean clone:

```bash
cp .env.example .env
make up      # build, start, migrate and seed - a few minutes on a first run
```

Then follow it in the browser:

1. **Open the Admin Console** at <http://localhost:4200> and log in as
   `user-admin` / `demo-password`. The landing screen is the role matrix.
2. **Find the role.** Search for `doctor` and select it. Its canonical
   identifier is `kc:tenant-a:patient-app:doctor` - the same string the
   authorization database is keyed by and a token normalises to (§7.5).
3. **Open the Business UI** at <http://localhost:4201> in a second tab and
   click into patient `patient-456`. The patient detail route is denied: the
   `patient.route.details` capability needs `person:read`, which the doctor
   role does not grant.
4. **Grant it.** Back in the console, filter the resource catalog for
   `person`, tick `read`, and press Save.
5. **Read the impact preview.** Before anything is written, the console lists
   the composite UI capabilities that resource-action affects -
   `patient.route.details` among them (§9.2). Confirm.
6. **Watch the Business UI.** Reload the patient page. Within a second or two
   the detail route renders. Nothing was rebuilt: the role matrix write bumped
   the tenant's permission revision, the outbox event invalidated exactly the
   affected cache keys, and the next decision saw the new data.
7. **Check that no policy moved.** In the console's revision and activation
   screen, the active root policy release is the same one as before the
   change.

Untick the permission and save again to put the demo back where it started.

The same sequence, executed rather than described:

```bash
make walkthrough                  # against the compose stack
bash scripts/k8s-walkthrough.sh   # the same script against deploy/k8s on kind
```

`scripts/tests/walkthrough.sh` drives the identical HTTP surface the two
browser tabs use, asserts each step, and times the convergence. CI runs it, so
"works exactly as written" is checked rather than claimed.

### Demo logins

Every demo user's password is `demo-password`, in both deployment paths.

| User | Use it for |
| --- | --- |
| `user-admin` | The Admin Console: role matrix, overrides, simulator, audit |
| `user-doctor` | A clinician with role-granted `patient_record` read and update |
| `user-doctor-revoked` | The same role, with a user REVOKE on `update` (ADR-003) |
| `user-clerk-granted` | No role grants; a user GRANT on `read` alone |
| `user-unassigned` | A valid login with no permissions at all |

## Exposed URLs

Three things are published: the two browser entry points and the identity
provider a browser has to be redirected to. Everything else - the PDP, the
ADS, the database - is reachable only from inside the compose network (§16.1).
The Administration Service serves the Admin Console and proxies its API calls
(ADR-008), so the console needs no port of its own.

| Service | Host URL | In-network address | Published? |
| --- | --- | --- | --- |
| Admin Console | <http://localhost:4200> | `admin-service:8081` | yes (`ADMIN_CONSOLE_PORT`) |
| Business UI | <http://localhost:4201> | `business-ui:80` | yes (`BUSINESS_UI_PORT`) |
| Keycloak | <http://localhost:8081> | `keycloak:8080` | yes (`KEYCLOAK_PORT`) - a login is a browser redirect |
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
make arch-test     # the four architectural invariants no compiler enforces
make smoke         # health checks and real decisions against a running stack
make walkthrough   # the guided walkthrough above, executed against a running stack
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

To run locally against `kind` or `minikube`, one command does the whole
sequence - cluster, KEDA, images, overlay, schema, seed, and the guided
walkthrough against the result:

```bash
bash scripts/k8s-walkthrough.sh                     # up, verify, tear down
K8S_WALKTHROUGH_KEEP=1 bash scripts/k8s-walkthrough.sh   # leave it running
```

Budget around 25 minutes for a first run: almost all of it is pulling
PostgreSQL, Keycloak and Redpanda into the node. A second run against the same
cluster takes about a minute.

By hand, the same steps are:

```bash
docker build -f deploy/cerbos/Dockerfile -t docker.io/cerbos-poc/cerbos-assets:dev .
docker build -f apps/ads/Dockerfile -t docker.io/cerbos-poc/ads:dev .
docker build -f apps/admin-service/Dockerfile -t docker.io/cerbos-poc/admin-service:dev .
docker build -f apps/resource-service/Dockerfile -t docker.io/cerbos-poc/resource-service:dev .
docker build -f apps/business-ui/Dockerfile -t docker.io/cerbos-poc/business-ui:dev .
kind load docker-image docker.io/cerbos-poc/cerbos-assets:dev docker.io/cerbos-poc/ads:dev \
  docker.io/cerbos-poc/admin-service:dev docker.io/cerbos-poc/resource-service:dev \
  docker.io/cerbos-poc/business-ui:dev
kubectl apply -k deploy/k8s/overlays/dev
```

The image names are fully qualified on purpose: a manifest's
`cerbos-poc/ads:dev` resolves as `docker.io/cerbos-poc/ads:dev`, and Podman
would otherwise build and load it as `localhost/cerbos-poc/ads:dev`, leaving
every pod in `ImagePullBackOff` beside an image that is already on the node.

**Nothing about the walkthrough changes on Kubernetes**: the same demo logins,
the same `/api/admin` and `/api/ads` paths, and the same two browser entry
points once `admin-service` and `keycloak` are forwarded (`kubectl port-forward
svc/admin-service 4200:8081`, `svc/keycloak 8081:8080`). Forward PostgreSQL on
something other than 5432 if the host already has one - `port-forward` binds
`::1` only rather than failing, and a migration then reaches the wrong
database.

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
