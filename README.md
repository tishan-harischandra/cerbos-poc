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

Only the Admin Console is published to the host. Everything else is reachable
only from inside the compose network, which is what keeps the PDP private.

| Service | Host URL | In-network address | Published? |
| --- | --- | --- | --- |
| Admin Console | <http://localhost:4200> | `admin-console:80` | yes (`ADMIN_CONSOLE_PORT`) |
| ADS health | <http://localhost:4200/api/ads/healthz> | `ads:8080/healthz` | via the console proxy only |
| ADS readiness | <http://localhost:4200/api/ads/readyz> | `ads:8080/readyz` | via the console proxy only |
| ADS decisions | `POST /api/ads/internal/authz/check` | `ads:8080/internal/authz/check` | via the console proxy only |
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
apps/admin-console    Angular app: live platform status
libs/permissioncontext  Assembles permissionContext data; never a verdict
libs/cerbosclient     Long-lived gRPC channel to the PDP
deploy/cerbos         Cerbos config, the served policy bundle and the ADR-003 control
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
