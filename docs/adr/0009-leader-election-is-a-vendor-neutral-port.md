# ADR-009: Leader election is a vendor-neutral port with selectable adapters

Two workloads need singleton behaviour across replicas: the policy release
pipeline and the outbox publisher. The first already had leader election, but
private to `apps/policy-controller/internal/leader` and hard-wired to
`pgx.Connect`, so it cannot run under the `oracle` profile at all; the second
had none, so every `admin-service` replica publishes every outbox row and the
duplication scales with an HPA that is about to be driven by console traffic
(ADR-008). We therefore extract `libs/leaderlock`: a provider-neutral port with
a composition-root factory selected by `LEADER_ELECTION_TYPE`, modelled exactly
on the `libs/idpdirectory` seam (ADR-004), so an SRE picks the coordination
backend their platform already runs instead of the one this repo happened to
reach for.

## The interface

```go
Run(ctx context.Context, election Name, onElected func(leaderCtx context.Context)) error
```

`leaderCtx` is cancelled the moment leadership is lost. `outbox.Loop.Run` and
the policy controller's poll loop already return cleanly on `ctx.Done()`, so
neither needs to know an election happened — renewal, backoff and loss
detection stay behind the seam. An `Acquire`/`Renew`/`Release` handle would put
a renewal ticker in every caller, and an `IsLeader() bool` is stale by the time
it is read.

Elections are named (`outbox-publisher`, `policy-controller`) rather than
identified by a magic key. Each adapter maps the name into its own key space:
an int64 hash for advisory locks, a Lease name, a Redis key, a table primary
key.

## Adapters

| `LEADER_ELECTION_TYPE` | Mechanism | Guarantee |
|---|---|---|
| `PG_ADVISORY` | `pg_try_advisory_lock` on a dedicated session | Session-scoped: no TTL, no renewal, no split-brain |
| `DATABASE` | `leader_lock` table, TTL and expiry on the database clock | Lease: split-brain window |
| `K8S_LEASE` | `coordination.k8s.io/v1` Lease | Lease: split-brain window |
| `REDIS` | `SET NX PX` with compare-and-extend renewal | Lease: split-brain window |
| `SINGLE` | Always leader, no coordination | None — for single-replica and test use |

There is deliberately **no default**. Unlike `IDP_TYPE`, where a wrong value
fails loudly, an unset leader-election type would fail silently as N
concurrent leaders, so the factory refuses to start.

`PG_ADVISORY` and `DATABASE` are kept as separate types rather than one
dialect-dispatching `DATABASE`, because their guarantees genuinely differ; a
single name covering both would hide a split-brain window behind a
configuration value that looks identical on the two dialects.

## Consequences

- **The port promises only the weakest member's guarantee.** Lease-based
  election is not mutual exclusion: a paused or partitioned leader can overlap
  with its successor. This is acceptable for both current consumers — outbox
  delivery is already at-least-once with an idempotent invalidation consumer,
  and the release pipeline installs atomically — but the port documentation
  must state it, because `PG_ADVISORY` over-delivering is not a promise the
  interface makes.
- No fencing tokens. Nothing downstream here can validate an epoch, and an
  unused fencing token in the interface is worse than an explicit warning.
- `K8S_LEASE` is implemented against the Kubernetes REST API using the mounted
  ServiceAccount token and `resourceVersion` optimistic concurrency, not
  `client-go`. This matches how `libs/cerbosclient` and the Keycloak adapter
  are already built and keeps a very large dependency out of the tree.
- `DATABASE` needs a Liquibase changeset and must pass the shared contract
  suite on both PostgreSQL and Oracle, inheriting the dual-dialect constraint.
- Redis becomes an optional dependency: a `redis` compose profile and an
  optional kustomize component, mirroring the existing `oracle` profile and
  `policy-release` component.
- The concrete adapters join `ConcreteAdapterPackages` in
  `tests/architecture/adapterimports.go`, so no consumer may name one.
- `ensureConn`'s transparent reconnect (a half-open socket after a PostgreSQL
  restart silently wedged the controller, found by the issue #26 chaos suite)
  must survive into the `PG_ADVISORY` adapter along with its regression test.
