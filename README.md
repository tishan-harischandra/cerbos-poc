# Cerbos multi-tenant authorization prototype

A working prototype of the design in
[`docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md`](docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md).
The product requirements live in
[`docs/PRD_Cerbos_Authorization_Prototype.md`](docs/PRD_Cerbos_Authorization_Prototype.md).

This is the walking skeleton: an Nx workspace driving Go and Angular in one
dependency graph, one Go service, one Angular app, and a `docker compose` stack
with PostgreSQL and the Cerbos PDP. There is no authorization logic yet.

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

## Workspace layout

```
apps/ads              Go service: /healthz and /readyz
apps/admin-console    Angular app: live platform status
deploy/cerbos         Cerbos config and the policy directory (one file per resource)
scripts/              Container-backed toolchain helpers and infrastructure tests
```

## Development

```bash
make test          # every project's tests plus the compose topology contract
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
