# Measured findings

Numbers, the configuration that produced them, and what each one means. A
measurement without its host and its configuration is an anecdote, so every
table below names both. Where something has not been measured yet, it says so
rather than quoting the design's target back as if it were a result.

## The host these numbers come from

| Property | Value |
|---|---|
| CPU | 16 cores, x86-64 |
| Memory | 31 GiB total, ~8 GiB available during the run |
| Disk | NVMe, 197 GiB free |
| OS | Linux |
| Container runtime | Podman with the docker-compose provider |
| Deployment | `make up`, docker compose, single replica per service |
| Population | The demo profile (`libs/assignmentstore/demoseed`), not the §15 load model |

**This is a developer workstation running the whole control plane and the
measuring process at once.** Every number here is a floor, not a capacity
statement.

## Policy tree size and compile time (§19.1, ADR-006)

| Measurement | Value |
|---|---|
| Resource policies | 156 (one file per resource, ADR-006) |
| Test files | 311 |
| Total policy tree, on disk | 5.0 MiB, 467 YAML files |
| Assertions in the served suite | 11,303 |
| Assertions in the ADR-003 control experiment | 3 |
| `make policy-test`, wall clock | ~16 s (compile + both suites, in a container) |

**Finding: the exhaustive matrix does not need sharding.** The PRD anticipated
splitting the §19.1 matrix across CI jobs and estimated ~6,600 cases. The real
number is 11,303 - the estimate was low - and the whole suite still compiles
and runs in about sixteen seconds, so it stays one CI job. That is the number
to watch: if catalog growth pushes this into minutes, sharding becomes the
answer, and reducing coverage never does.

## Seed duration and footprint (demo profile)

| Measurement | Value |
|---|---|
| Liquibase changesets applied | 19, in one pass |
| Rows written by the migration | 1,497 (the resource and action catalog, and the 400 UI capability definitions) |
| Authorization database size after seeding | 8.6 MiB |
| `authorization_action` rows | 924 |
| `ui_capability_definition` rows | 400 |
| `authorization_resource` rows | 154 |
| `role_permission` rows | 8 |
| `fhir_resource` rows | 6 |
| `make up` (build cached, to healthy and seeded) | ~2 min |

The catalog dominates the demo footprint: the assignment data is a handful of
rows, and everything else is the released resource and capability catalog,
which is the same at any population size.

**Not yet measured: the §15 load profile's seed.** 600,000 Keycloak users and
42M role mappings need the 8 GiB the preflight demands, and this host was
running the whole stack plus a kind cluster during this slice. See
[`LOAD_TESTING.md`](LOAD_TESTING.md) for how to run it.

## Decision latency (warm path)

| Measurement | Value |
|---|---|
| Method | 200 sequential `POST /internal/authz/check` calls after 20 warm-up calls, measured client-side with `curl -w %{time_total}` |
| Path | host -> admin-service (Go proxy) -> ADS -> Cerbos gRPC -> back |
| Concurrency | 1 |
| p50 | 3.4 ms |
| p95 | 5.6 ms |
| p99 | 7.0 ms |
| max | 9.5 ms |

**Finding: the warm path has real headroom against §15.3's p95<15 ms.** This
is a single-connection, single-VU measurement including the console proxy hop
and the client's own TLS-free HTTP overhead, so it is not the §15.3 number -
that one is `warm_decision_latency_ms` under 1,000 VUs, measured inside k6.
What it does establish is that the floor is a few milliseconds, not tens, so a
poor result under load would be a concurrency finding rather than a
per-decision cost.

**Not yet measured: throughput.** Sequential requests measure latency only. A
requests-per-second number requires the k6 suite.

## ADS cache hit ratio (§17.1)

| Cache | Hits | Misses | Ratio |
|---|---|---|---|
| `role_permissions` | 225 | 7 | 97.0% |
| `user_overrides` | 225 | 7 | 97.0% |

Measured over the walkthrough plus the 220 decisions above, against one
tenant, one role and one resource - a keyspace of one. **This number will not
survive the load model** and is not meant to: with 600,000 users and a uniform
access distribution, the override cache is expected to evict continuously.
That measurement is a genuine sizing output and is listed as a known scale
risk in the PRD.

Cache misses at all in a single-key workload come from the invalidation the
walkthrough itself causes: each role matrix save drops the entry, as it should.

## Permission convergence, end to end (§10, §15.3)

Measured by `scripts/tests/walkthrough.sh`, which polls the capability
snapshot after a real role matrix save until it flips:

| Direction | Observed |
|---|---|
| Grant visible to the Business UI | 1 s |
| Withdrawal visible to the Business UI | 2 s |

Both inside §15.3's five-second objective, on the ordinary path: the
Administration Service commits the write and the outbox row in one
transaction, the publisher hands it to Redpanda, and the ADS's consumer
invalidates exactly the keys the event names. The reconciler (2 s interval)
would have repaired it anyway had the message been lost - which is what the
Kafka-outage chaos scenario exercises.

**No policy release occurs in that path.** The walkthrough asserts the root
policy revision is the same string before and after (`root-v1.4.0`), which is
the whole claim of §3.1: assignments are data, not policy files.

## The same walkthrough on Kubernetes

`scripts/k8s-walkthrough.sh` runs `scripts/tests/walkthrough.sh` unchanged
against `deploy/k8s/overlays/dev` on a single-node kind cluster.

| Property | Value |
|---|---|
| Cluster | kind, one node, Kubernetes v1.30 |
| Overlay | `deploy/k8s/overlays/dev`, one replica per service |
| Prerequisite installed by the script | KEDA 2.15.1 (every base workload carries a `ScaledObject`) |
| Access | `kubectl port-forward` for admin-service, Keycloak and PostgreSQL |
| Result | Every step passed, identically to compose |
| Convergence, grant | 1 s |
| Convergence, withdrawal | 1 s |
| Root policy revision | `root-v1.4.0`, unchanged across the change |
| First run, wall clock | ~25 min, almost all of it image pulls |
| Second run against the same cluster | ~1 min |

Two environment-shaped problems this surfaced, both now handled by the script
and worth knowing about for any similar run:

- **Podman names a locally built image `localhost/<name>`**, while a manifest's
  unqualified `tenant-a/ads:dev` resolves to `docker.io/tenant-a/ads:dev`.
  Every pod sat in `ImagePullBackOff` next to an image that was already on the
  node. The script builds and loads fully qualified names, which is a no-op
  under Docker.
- **`kubectl port-forward` does not fail when it cannot bind 127.0.0.1.** With
  a PostgreSQL already on the host's 5432 it bound `::1` only and looked
  healthy, and the migration authenticated against the host's database
  instead. The script forwards PostgreSQL on 55432.

**No difference in steps, URLs or credentials.** The same demo logins, the
same `/api/admin` and `/api/ads` paths, the same ports once forwarded - which
is the point of publishing nothing but the Administration Service in either
path.

## Observed bottlenecks

- **Image pulls and cold starts dominate every first run.** Measured: 3m11s to
  pull `postgres:16-alpine` into the kind node, and over 17 minutes of
  queueing behind other pulls before it started. Nothing the platform is
  responsible for, which is why the rollout waits are generous (900 s) and why
  a second run against the same cluster takes about a minute.
- **`kubectl port-forward` is a single-connection tunnel** and will be the
  limit in any Kubernetes load run long before the platform is. See
  [`LOAD_TESTING.md`](LOAD_TESTING.md).
- **The capability snapshot endpoint costs one store read per resolved
  instance target.** `StoreTargetResolver` reads the instance to obtain its
  status (DEVIATIONS A4). Leaves are deduplicated by resolved target, so a
  capability set sharing a target reads once - but a page of rows with
  distinct instance targets reads once each, and that read is uncached. It is
  the first thing to look at if `capability_row` is slow under load.
- **The policy suite is CPU-bound in one container.** Sixteen seconds on
  sixteen cores is not sixteen seconds on a two-core runner.

## Organization-scoped tokens without a browser (issue #75)

The realm/tenant/organization rework (#74) puts one load-bearing assumption on
the critical path: that the 1,000-VU protocol-level load model can obtain an
organization-scoped token by direct grant, with no browser and no custom
Keycloak authenticator SPI in the path. This was spiked against a real
Keycloak 26.4 (the `organization` feature is Preview and must be enabled with
`--features=organization`; `deploy/keycloak/realm-tenant-a.json` now
declares two organizations and one membership; `scripts/tests/org-scope-spike.sh`
is the committed, repeatable form of this investigation).

| Question | Answer |
|---|---|
| Does `grant_type=password` with `scope=organization:<alias>` populate an `organization` claim? | **Yes.** Confirmed on a direct grant against `patient-app`, no browser, no authenticator flow involved. |
| What shape is the claim? | A JSON array of alias strings, e.g. `"organization": ["north-hospital"]` - not a map, not a bare scalar. Parse it as a list of one. |
| What happens when the requested alias is not a membership? | Keycloak does **not** refuse the grant. It returns 200 with a token whose granted `scope` silently drops the `organization:<alias>` entry and whose `organization` claim is absent entirely. A caller that only checks the HTTP status will not notice. |
| Does this apply to an administrator with no membership at all? | Same silent omission - holding the realm role `admin` does not let a direct grant acquire an organization scope by simply asking for it. |
| Can organization membership be declared in the static realm-import JSON? | Only with `{"username": "<user>"}` member objects. `{"id": "<user>"}` deserializes but then fails import with a null-pointer deep in Keycloak's organization importer (`Cannot invoke "UserModel.getId()" because "user" is null`) - a real bug/limitation in 26.4's realm importer, not a fixture mistake. Any future seed/onboarding code that adds members programmatically must go through the Admin REST API (`POST .../organizations/{id}/members` with a bare user-id string body), not a hand-written import block. |

**Finding: the load model in #74 survives unchanged.** Protocol-level virtual
users can obtain a real organization-scoped token via direct grant exactly as
the PRD's user story #41 assumes. No redesign is required before slice #87.

**Finding: "requesting an org you don't belong to" is enforced by omission,
not rejection.** The decision service's own refusal of an unscoped token
(PRD decision: "a token carrying neither an active organization nor the
tenant-wide marker is refused") is therefore load-bearing on its own -
Keycloak's token endpoint will not do that refusing for it. Slice #78 and the
decision-service tests must check for the *absence* of a usable organization
claim, not for a non-200 status from Keycloak.

## What has not been measured

Stated plainly, so nobody mistakes an absence for a pass:

| Not measured | What it needs |
|---|---|
| The §15.3 1,000-VU run, either deployment | 8 GiB free and an otherwise idle host; `make loadtest` |
| Full-scale seed duration and footprint | The same |
| Throughput (requests per second) | The k6 suite; sequential curl cannot produce one |
| Oracle-side latency | `make db-test-dual` proves portability, not performance |
| Behaviour above one replica per service | The k8s overlays scale, but no multi-replica measurement was taken |
