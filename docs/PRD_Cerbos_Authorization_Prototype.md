# PRD — Cerbos Multi-Tenant Authorization Platform: Working Prototype

| Field | Value |
|---|---|
| Source design | `docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md` (v1.3, 2 August 2026) |
| Status | Approved for breakdown into implementation slices |
| Target | Working prototype runnable with `docker compose up` |
| Repository | `tishan-harischandra/cerbos-poc` |

---

## Problem Statement

The v1.3 design document describes a complete target architecture for a dedicated Cerbos
Policy Decision Point serving many tenants and hospitals from one installation. It is a
strong document, but it is *only* a document. Nobody can currently run it, click through
it, break it, or measure it.

That leaves several load-bearing claims entirely unproven:

- **Precedence correctness.** The design asserts that `mandatory deny > user REVOKE > user
  GRANT > any role grant > default deny` can be made deterministic inside Cerbos by
  funnelling every rule through one synthetic evaluation role (`sys:permission-evaluator`,
  §6.4, ADR-003). Cerbos' native multi-role conflict resolution would otherwise let an
  allow from one role defeat a deny from another. Nobody has demonstrated this actually
  behaves as claimed against a real PDP.
- **Composite UI capability performance.** The design replaces per-route PDP calls with
  backend-evaluated composite capabilities (§12, ADR-005) whose leaves are deduplicated and
  batched. The claim that this turns "one PDP call per route, component and button" into
  "roughly one batched evaluation per module snapshot" is untested at realistic catalog
  size.
- **Scale.** §15.3 sets warm decision latency at p95 ≤ 15 ms and p99 ≤ 30 ms, and permission
  convergence at 99% of updates visible on all healthy replicas within five seconds. Those
  are aspirations with no measurement behind them, and no sizing guidance exists because
  §14.3 explicitly defers it to load testing.
- **Catalog scale.** Every worked example in the design uses three resources
  (`patient_record`, `clinical_note`, `prescription`). Real deployments carry a full FHIR
  resource catalog. Whether the one-policy-file-per-resource rule of ADR-006 survives
  contact with the complete FHIR R6 resource set — in compile time, PDP memory, release
  archive size and CI duration — is unknown.
- **Database portability.** The design names PostgreSQL throughout. Real installations
  require Oracle. No schema exists, in either dialect, and the four JSON columns
  (`expression_json`, `before_json`, `after_json`, outbox `payload`) plus boolean and
  identity-column semantics are exactly where portability quietly fails.

Meanwhile an engineer arriving at this repository finds one Markdown file. There is no
monorepo, no service skeleton, no schema, no policy tree, no seed data, no load harness,
and no way to answer "does this design work?" other than by reading it again.

## Solution

Build a complete, runnable reference implementation of the v1.3 design as an Nx monorepo,
with Go backend services and Angular frontends, that a newcomer can bring up with a single
`docker compose up` and that a performance engineer can drive to full stated scale with a
single `make loadtest`.

The prototype is *architecturally faithful*, not architecturally simplified. Every component
in Figure 1 exists as a real running process, including the parts that only make sense in
a closed-network Kubernetes deployment: Gitea genuinely hosts the root policy repository,
the Policy Sync and Release Controller genuinely polls an immutable tag, genuinely runs
`cerbos compile` and the policy test suite, genuinely builds an immutable archive, and
genuinely activates it against the Cerbos fleet through the pod-local Admin API — refusing
to mark a release active until every healthy replica reports the target revision.

Two experiences come out of it:

1. **A demo you can click.** `docker compose up` yields a small-seed installation with the
   full resource catalog, all 400 UI capabilities, a complete eight-module Admin Console,
   and a Business UI that demonstrates every capability-rendering mechanism in §12. An
   administrator can grant a role permission, watch the impact preview list the composite
   capabilities it affects, save it against an expected revision, and see the Business UI
   change behaviour within seconds — with no Cerbos policy rebuild anywhere in that path.

2. **A measurement you can trust.** `make loadtest` seeds the full stated population — 600,000
   Keycloak users across 5 tenants and 20 hospitals, 250 canonical roles, 70 roles per user
   (42 million role mappings), ~150,000 user overrides — and drives 1,000 concurrent k6
   virtual users through both endpoint authorization and capability-snapshot rendering
   paths, with the §15.3 targets encoded as hard k6 thresholds so the run returns a verdict
   rather than a chart.

The prototype's job is to turn every assertion in v1.3 into something that either passes or
fails in CI.

## User Stories

### Getting started and running the prototype

1. As a **backend engineer new to the project**, I want to clone the repository and run
   `docker compose up`, so that I have the entire authorization platform running locally
   without reading a setup guide first.
2. As a **backend engineer**, I want the default `docker compose up` to complete in minutes
   on a modest laptop, so that trying the prototype is not an all-afternoon commitment.
3. As a **backend engineer**, I want a single `README` that states exactly which URLs to
   open and which demo credentials to use, so that I can reach a working screen immediately
   after startup.
4. As a **backend engineer**, I want every service to expose a health endpoint and for
   compose to declare correct `depends_on` health conditions, so that the stack comes up in
   a valid order instead of crash-looping until things happen to be ready.
5. As a **backend engineer**, I want the Nx task graph to build only what I changed, so that
   iterating on one Go library does not rebuild both Angular applications.
6. As a **backend engineer**, I want the Go services and Angular apps to share one Nx
   dependency graph, so that I can see what a change to a shared library will affect before
   I make it.
7. As a **platform operator**, I want `make down` and `make clean` to fully tear down
   containers and volumes, so that I can return to a known-empty state after experimenting.
8. As a **platform operator**, I want the full-scale load profile to be a separate compose
   profile from the default demo, so that ordinary use of the prototype does not require
   tens of gigabytes of disk and a long seed.
9. As a **platform operator**, I want a preflight check that refuses to start the full-scale
   profile when the host lacks sufficient RAM or disk, so that I get a clear error instead
   of an OOM kill an hour into a seed.

### Resource and action catalog

10. As a **tenant administrator**, I want to browse the complete FHIR R6 resource catalog
    with display names, business domain groupings, risk metadata and catalog revision, so
    that I can find the resource I need to configure.
11. As a **tenant administrator**, I want every resource to expose the same six actions
    (`create`, `read`, `update`, `delete`, `list`, `assign`), so that the permission matrix
    is predictable and I do not have to learn a different action vocabulary per resource.
12. As a **tenant administrator**, I want each action to declare whether its context is
    `COLLECTION` or `INSTANCE`, so that I understand whether a grant affects a listing or a
    specific record.
13. As a **backend engineer**, I want the resource catalog to be generated from a single
    committed FHIR resource manifest, so that the catalog, the Cerbos policies, the JSON
    schemas and the database seed can never disagree about which resources exist.
14. As a **backend engineer**, I want the manifest to record why each resource is included or
    excluded, so that the catalog's composition is auditable rather than arbitrary.
15. As a **release manager**, I want CI to fail if a catalog action has no corresponding rule
    in the generated Cerbos policy, so that catalog-policy drift (§21) is caught before
    release rather than discovered as a silent deny in production.

### Role permission matrix

16. As a **tenant administrator**, I want to select a tenant and a role and see the full
    resource-action matrix for that role, so that I can understand what the role currently
    grants.
17. As a **tenant administrator**, I want to enable and disable individual resource-action
    permissions with checkboxes, so that adjusting a role is a direct, obvious operation.
18. As a **tenant administrator**, I want a cleared checkbox to mean "no grant" and never
    "explicit deny", so that removing a role permission does not accidentally override
    another role that legitimately grants it.
19. As a **tenant administrator**, I want resources grouped by business domain and actions
    grouped by collection versus instance context, so that a catalog of this size remains
    navigable.
20. As a **tenant administrator**, I want to search and filter the matrix by resource name,
    so that I can reach a specific resource without scrolling through the whole catalog.
21. As a **tenant administrator**, I want the save operation to carry the revision I loaded,
    so that my change is rejected rather than silently overwriting a colleague who edited
    the same role while I was working.
22. As a **tenant administrator**, I want a clear, actionable error when my expected revision
    is stale, telling me what changed and offering to reload, so that concurrency conflicts
    are recoverable rather than confusing.
23. As a **tenant administrator**, I want role permissions to apply across all hospitals in
    my tenant by default, so that I am not forced to repeat the same configuration per
    hospital.
24. As a **tenant administrator**, I want to see inherited or composite IdP roles as
    informational context while assignments persist against stable canonical role
    identifiers, so that renaming a role in the identity provider does not silently detach
    its permissions.
25. As a **tenant administrator**, I want roles whose canonical identifier no longer resolves
    in the identity provider to be flagged for remediation rather than silently dropped, so
    that deleted IdP roles surface as an explicit problem.

### User-specific overrides

26. As a **tenant administrator**, I want a tri-state control offering Inherit, Grant and
    Revoke for a specific user, so that the three distinct states of the model are directly
    representable.
27. As a **tenant administrator**, I want user overrides to be scoped to a specific tenant
    *and* hospital, so that an exception granted at one site does not leak to another.
28. As a **tenant administrator**, I want a user REVOKE to defeat every role grant the user
    has, so that I can remove a specific capability from one person without restructuring
    their roles.
29. As a **tenant administrator**, I want a user GRANT to allow an action even when no role
    grants it, so that I can handle a legitimate exception without creating a bespoke role.
30. As a **tenant administrator**, I want to see the underlying role result and the resulting
    effective result side by side before I save, so that I understand exactly what my change
    will do.
31. As a **tenant administrator**, I want a warning when my override merely duplicates the
    existing role result, so that I do not accumulate overrides that have no practical
    effect.
32. As a **tenant administrator**, I want to record a mandatory reason for every override, so
    that a future reviewer can understand why the exception exists.
33. As a **tenant administrator**, I want validity start and optional expiry dates on
    overrides, so that temporary access genuinely expires instead of becoming permanent by
    neglect.
34. As a **tenant administrator**, I want high-risk direct grants to default to a bounded
    expiry, so that the safe choice is the default choice.
35. As a **tenant administrator**, I want expired overrides to stop taking effect immediately
    and be excluded from evaluation, so that expiry is a real control and not merely a
    label.
36. As a **tenant administrator**, I want revocation to be a first-class action rather than
    something I emulate by removing IdP roles, so that the intent is recorded explicitly and
    survives role changes.

### Composite UI capabilities

37. As a **frontend engineer**, I want UI capabilities defined as versioned `allOf` / `anyOf`
    expressions over resource-action leaves, so that a route, component or button can
    require several permissions across several resources without duplicating that logic in
    Angular.
38. As a **frontend engineer**, I want the backend to return already-evaluated capability
    decisions, so that the browser never parses expressions or implements permission
    precedence.
39. As a **frontend engineer**, I want a module-level capability snapshot at login and on
    tenant or hospital switch, so that route guards can resolve synchronously from signals
    without a network call per navigation.
40. As a **frontend engineer**, I want an instance-level snapshot fetched once when a page
    resource loads and reused across child routes and tabs, so that navigating within a
    patient context does not re-query the PDP repeatedly.
41. As a **frontend engineer**, I want snapshots split by lazy-loaded module rather than one
    installation-wide payload at login, so that the snapshot stays small as the capability
    catalog grows.
42. As a **frontend engineer**, I want row-level capability decisions returned in one batch
    with the list data, so that rendering a table of row menus does not issue one request
    per row.
43. As a **frontend engineer**, I want a `capabilityGuard` that reads the store synchronously
    and redirects to a forbidden route, so that guard implementation is one line per route.
44. As a **frontend engineer**, I want a structural directive that shows or hides a component
    by capability key, so that conditional rendering is declarative in the template.
45. As a **frontend engineer**, I want the snapshot to carry the authorization revision and
    capability catalog revision, so that the client can detect staleness and refresh.
46. As a **frontend engineer**, I want a 403 from a business endpoint to invalidate the
    affected snapshot and retry exactly once before surfacing a final denial, so that a
    stale snapshot produces a self-healing refresh rather than a spurious error.
47. As a **clinician**, I want controls I am not permitted to use to be hidden or disabled
    rather than failing after I click them, so that the interface reflects what I can
    actually do.
48. As a **security reviewer**, I want protected data never fetched and then hidden with CSS,
    so that hiding is a UX affordance and the owning endpoint is the actual boundary.
49. As a **security reviewer**, I want capability composition to be incapable of producing an
    allow that was absent from all required leaf decisions, so that the aggregation layer
    can only ever be more restrictive, never less.
50. As a **tenant administrator**, I want to see which composite UI capabilities depend on a
    given resource-action permission, so that I understand the blast radius before I toggle
    it.
51. As a **tenant administrator**, I want an impact preview when saving a role matrix change,
    listing capabilities that may become enabled or disabled, so that a one-checkbox change
    does not have surprising consequences.
52. As a **release manager**, I want CI to reject capability definitions with empty `allOf` or
    `anyOf` arrays, unknown resources, unknown actions, or circular references, so that a
    broken catalog cannot reach a release.

### Runtime authorization

53. As a **backend engineer**, I want backend APIs to re-authorize every request against
    trusted, server-loaded resource state, so that the browser's rendering decisions are
    never the security boundary.
54. As a **backend engineer**, I want only an explicit Cerbos `EFFECT_ALLOW` to permit an
    operation, so that any error, timeout or ambiguity fails closed.
55. As a **backend engineer**, I want the Authorization Decision Service to construct the
    `permissionContext` and be the only component permitted to do so, so that trusted
    permission data cannot be injected from outside.
56. As a **security reviewer**, I want tenant and hospital identifiers taken from verified
    identity and server-side selection state and never from browser-supplied values, so that
    the tenant isolation invariant cannot be bypassed by a crafted request.
57. As a **security reviewer**, I want every resource policy to require
    `principal.tenantId == resource.tenantId`, so that tenant isolation is enforced by the
    PDP itself rather than by application discipline.
58. As a **security reviewer**, I want the synthetic evaluation role to be added only by the
    ADS after token verification, and any token presenting a reserved `sys:` role prefix to
    be rejected outright, so that the precedence mechanism cannot be hijacked.
59. As a **backend engineer**, I want the ADS to batch Cerbos checks within the documented
    request limits and chunk automatically when a request would exceed them, so that large
    list pages and rich capability sets do not fail or silently truncate.
60. As a **backend engineer**, I want gRPC channels to Cerbos to be long-lived and reused, so
    that connection setup does not appear in decision latency.
61. As a **backend engineer**, I want the ADS to keep in-process caches of role matrices and
    user overrides, so that PostgreSQL is absent from the warm authorization path.
62. As a **backend engineer**, I want a cache miss to read through to the database and
    populate the cache, so that a cold replica recovers without operator intervention.
63. As a **backend engineer**, I want the ADS caches to be bounded, so that 600,000 distinct
    users cannot exhaust replica memory.
64. As a **security reviewer**, I want a database outage to leave warm cached decisions
    serving while cache misses fail closed, so that degradation is graceful and never
    permissive.
65. As a **backend engineer**, I want the decision path to log the application correlation
    ID, the Cerbos call ID, the permission revision and the decision source, so that any
    decision can be reconstructed after the fact.
66. As a **clinician**, I want a locked record to be unmodifiable regardless of my roles or
    any override granted to me, so that mandatory platform rules genuinely cannot be
    bypassed.

### Permission propagation

67. As a **tenant administrator**, I want my saved change to be visible in the runtime within
    a few seconds, so that administration feels immediate.
68. As a **backend engineer**, I want the assignment write, the audit event, the revision
    increment and the outbox insert to occur in one database transaction, so that they can
    never diverge.
69. As a **backend engineer**, I want a `PermissionChanged` event published from the outbox to
    Kafka, so that invalidation is push-based and low latency.
70. As a **backend engineer**, I want each ADS replica to invalidate only the affected cache
    keys, so that one user's override change does not cold-start the whole cache.
71. As a **platform operator**, I want a periodic revision reconciler that compares cached and
    database revisions, so that a missed Kafka event self-heals rather than leaving stale
    permissions indefinitely.
72. As a **platform operator**, I want permission writes to remain committed and correct
    during a Kafka outage, with convergence catching up on recovery, so that the message bus
    is never the source of truth.
73. As a **security reviewer**, I want revocation latency measured and reported separately
    from general convergence, so that the more dangerous direction of change is held to its
    own objective.
74. As a **platform operator**, I want current permission revision and cache convergence state
    visible in the Admin Console, so that I can confirm a change has propagated.

### Root policy lifecycle

75. As a **release manager**, I want root policies, schemas, tests and the catalog to live in
    one Git repository and be released as an immutable tag, so that what is running is
    always traceable to an exact commit.
76. As a **release manager**, I want the Policy Controller to poll the repository rather than
    receive webhooks, so that the design works in a closed network where Git cannot initiate
    inbound connections.
77. As a **release manager**, I want a release blocked unless the catalog validates, schemas
    validate, `cerbos compile` succeeds, and the full policy test suite passes, so that an
    invalid policy set can never activate.
78. As a **release manager**, I want generated tenant-isolation, hospital-isolation and
    precedence invariants executed as part of the release gate, so that the platform's core
    guarantees are re-proven on every release.
79. As a **release manager**, I want each validated release to produce an immutable archive
    and a separate manifest with a checksum, so that the artifact is verifiable and
    reproducible.
80. As a **platform operator**, I want the archive installed atomically on each PDP replica
    followed by an explicit local store reload, so that no replica ever serves a
    half-written policy set.
81. As a **platform operator**, I want a release marked active only once every healthy replica
    reports the target revision, so that partial rollouts are visible failures rather than
    silent inconsistency.
82. As a **platform operator**, I want to roll back to the previous immutable archive without
    touching assignment data, so that recovery is fast and does not risk permission loss.
83. As a **platform operator**, I want the currently active root revision to remain serving
    when the Git server is unavailable, so that source control is not a runtime dependency.
84. As a **security reviewer**, I want the Cerbos Admin API reachable only from a pod-local
    channel and never through ingress, so that its documented instability and single
    basic-auth credential are not externally exposed.

### Identity provider integration

85. As a **platform operator**, I want the active identity provider selected by environment
    configuration, so that switching providers is a deployment decision and not a code
    change.
86. As a **backend engineer**, I want every consumer to depend on the `IdentityDirectory`
    abstraction and never on a concrete adapter, so that dependency inversion is structurally
    enforced rather than merely intended.
87. As a **tenant administrator**, I want to search users and roles from the Admin Console with
    pagination, so that a directory of 600,000 users remains usable.
88. As a **security reviewer**, I want all identity provider administrative calls made
    server-side with a least-privileged service account, so that administrative credentials
    never reach the browser.
89. As a **backend engineer**, I want canonical role identifiers to follow one documented
    format and be produced identically by token normalization and by the administration
    path, so that the matrix and the runtime always agree on what a role is.
90. As a **platform operator**, I want an IdP diagnostics screen showing the selected provider,
    connectivity and role and token mapping, so that misconfiguration is diagnosable without
    reading logs.
91. As a **platform operator**, I want the Admin Console's user and role search to degrade
    visibly while runtime authorization continues unaffected during an IdP admin API outage,
    so that an administration problem is not a production outage.
92. As a **clinician**, I want to log in through standard OIDC and be issued a token carrying
    my roles, so that authentication is conventional and no bespoke login exists.

### Database and portability

93. As a **database engineer**, I want one set of Liquibase changelogs that applies cleanly to
    both PostgreSQL and Oracle, so that there is a single schema definition rather than two
    that drift.
94. As a **database engineer**, I want dialect-specific changesets used only where the
    dialects genuinely diverge, and each such divergence to be explicit, so that portability
    exceptions are visible and reviewable.
95. As a **database engineer**, I want migrations to be idempotent and re-runnable, so that a
    partially applied migration can be safely retried.
96. As a **database engineer**, I want the same integration test suite executed against both
    engines in CI, so that portability is continuously proven and not assumed.
97. As a **backend engineer**, I want all persistence behind one store interface with separate
    driver implementations, so that no dialect-specific SQL leaks into service logic.
98. As a **backend engineer**, I want the Oracle driver to be pure Go, so that no Instant
    Client installation is required in any container image.
99. As a **database engineer**, I want unique keys and indexes on the tenant, role, user,
    resource, action and validity dimensions the design specifies, so that hot queries stay
    fast at seeded scale.
100. As a **security reviewer**, I want tenant predicates enforced on every administration
     query, so that a missing filter cannot expose another tenant's assignments.
101. As a **compliance officer**, I want audit history append-only and retained even when the
     current assignment row is updated in place, so that history cannot be rewritten.

### Load testing

102. As a **performance engineer**, I want the load environment seeded with 600,000 Keycloak
     users, 250 canonical roles and 70 roles per user, so that the measurement reflects the
     stated target population rather than a convenient subset.
103. As a **performance engineer**, I want that seed performed by bulk load rather than
     iterative API calls, so that setting up a run takes minutes rather than a day.
104. As a **performance engineer**, I want the seed to be deterministic and repeatable from a
     fixed random seed, so that two runs are genuinely comparable.
105. As a **performance engineer**, I want each virtual user to authenticate once and then use
     refresh-token rotation, so that password hashing does not dominate the measurement while
     every token remains genuinely provider-issued.
106. As a **performance engineer**, I want access token lifespan tuned so refresh frequency is
     a small fraction of request volume, so that the identity provider is not the bottleneck.
107. As a **performance engineer**, I want a separate scenario that benchmarks the token
     endpoint alone, so that identity cost is attributable and separable from authorization
     cost.
108. As a **performance engineer**, I want 1,000 concurrent virtual users with a defined
     ramp-up, steady state and ramp-down, so that the run captures both transient and
     sustained behaviour.
109. As a **performance engineer**, I want the scenario mix to cover business endpoint
     authorization and all three capability snapshot shapes, so that both halves of the
     requirement are exercised in the same run.
110. As a **performance engineer**, I want the §15.3 objectives encoded as hard k6 thresholds,
     so that a run produces a pass or fail rather than a chart requiring interpretation.
111. As a **performance engineer**, I want ADS cache hit ratio, Cerbos engine time, database
     miss latency and Kafka consumer lag exported during the run, so that a threshold breach
     can be attributed to a component.
112. As a **performance engineer**, I want a Grafana dashboard prepared for the run, so that
     live behaviour is observable rather than only visible in a post-run summary.
113. As a **performance engineer**, I want permission convergence measured under load by
     mutating permissions mid-run and timing visibility, so that the five-second objective is
     tested when it is hardest to meet.
114. As a **performance engineer**, I want a documented minimum host specification and a
     preflight check, so that a run either starts valid or refuses clearly.
115. As a **performance engineer**, I want results written to a versioned results directory
     with the git SHA and configuration, so that numbers remain traceable to what produced
     them.

### Observability, audit and resilience

116. As a **platform operator**, I want request rate, allow/deny/error rate and latency by
     resource and action exported as metrics, so that behaviour is visible in production
     terms.
117. As a **platform operator**, I want cache hit ratios exported per cache, so that a latency
     regression can be traced to a cache problem.
118. As a **platform operator**, I want current root revision and permission revision exported
     per replica, so that convergence problems are detectable.
119. As a **platform operator**, I want stale-revision duration and count of replicas behind
     target exported, so that a stuck replica is an alertable condition.
120. As a **compliance officer**, I want every administrative change recorded with actor,
     operation, before and after state, reason and correlation ID, so that changes are fully
     reconstructable.
121. As a **compliance officer**, I want to search audit history by actor, user, role, resource,
     action, tenant and date, so that access reviews are practical.
122. As a **compliance officer**, I want administration audit separated from authorization
     decision audit while remaining correlatable, so that the two concerns do not drown each
     other.
123. As a **security reviewer**, I want sensitive clinical attributes excluded from decision
     audit logs and request payload logging disabled, so that the authorization layer does
     not become a leak of protected health information.
124. As a **platform operator**, I want the system to continue serving when a PDP replica is
     killed, so that no sticky-session assumption exists.
125. As a **platform operator**, I want the system to behave correctly when Kafka is paused and
     then resumed, so that recovery is proven rather than hoped for.
126. As a **platform operator**, I want a failed policy rollout to leave the previous archive
     active and the release not marked active, so that a bad release is contained.

### Effective access simulation

127. As a **tenant administrator**, I want to simulate a decision for a specific user,
     hospital, resource, action and sample resource attributes, so that I can answer "why can
     this person do this?" without reproducing it in production.
128. As a **tenant administrator**, I want the simulator to run through the real runtime path
     rather than a reimplementation, so that its answer is authoritative.
129. As a **tenant administrator**, I want the simulator to show the decision source —
     mandatory rule, user revoke, user grant, or role grant — so that I understand which layer
     produced the outcome.
130. As a **tenant administrator**, I want to simulate composite capabilities and see the full
     requirement tree with each leaf decision, so that I can diagnose why a UI element is
     hidden.
131. As a **security reviewer**, I want end-user responses limited to a stable reason code
     while the administrative simulator exposes the full tree, so that failure evidence is
     filtered by audience.

## Implementation Decisions

### Repository and toolchain

- **Nx monorepo** driving both Go and Angular. Go projects via the Nx Go plugin; Angular
  natively. One dependency graph, affected-graph builds, one task runner.
- **Go** for all backend services. **Angular** for both frontends. This supersedes the Java
  interface sketch in §7.2 of the design; the `IdentityDirectory` contract is reproduced
  faithfully as a Go interface.
- **Liquibase** runs as a container. No JVM is required on the host or in any service image.
- A root `Makefile` provides the operator-facing verbs: `up`, `down`, `seed`, `loadtest`,
  `test`, `gen`, `clean`.

### Component topology

Every component in Figure 1 runs as a real compose service, with two deliberate deviations
recorded below.

| Component | Form | Notes |
|---|---|---|
| Cerbos PDP | Compose service, multiple replicas | gRPC on the hot path; Admin API pod-local only |
| Authorization Decision Service | Go service | Warm path: caches → Cerbos gRPC. No DB, Kafka or IdP call when warm |
| Authorization Administration Service | Go service | §9.4 API surface, transactional writes, outbox |
| Policy Sync and Release Controller | Go service | Leader-elected; polls Gitea; validates, archives, activates, verifies |
| Resource Service | Go service | Generic FHIR resource store and PEP; resolves `targetRef` |
| Identity Directory Adapter | **Library, not a service** | Deviation from Figure 1 — see below |
| Gitea | Compose service | Genuinely hosts the root policy repository and its tags |
| Kafka | Compose service (Redpanda) | Invalidation transport only, never source of truth |
| PostgreSQL | Compose service | Default authorization database |
| Oracle 23ai Free | Compose service, `oracle` profile | Not started by default |
| Keycloak | Compose service | Sole identity provider for the prototype |
| Admin Console | Angular application | All eight §9.1 modules |
| Business UI | Angular application | Minimal — see below |
| Prometheus + Grafana | Compose services, `observability` profile | Metrics and load-test dashboards |
| k6 | Compose service, `loadtest` profile | Protocol-level only |

**Deviation 1 — Identity Directory Adapter is a library.** §7.2 specifies a code-level
interface, and the adapter is never on the warm authorization path, so a network hop buys
nothing. Provider neutrality is preserved structurally: the library owns the port,
`KEYCLOAK` and `WSO2_IS` implementations sit behind it, and selection happens at the
composition root from environment configuration. **No consumer may reference a concrete
adapter type** — dependency inversion is enforced by an architecture test, not by
convention.

**Deviation 2 — Kubernetes is out of scope.** Helm charts, HPA, PodDisruptionBudgets,
topology spread and NetworkPolicy (§14) are not produced. The compose topology preserves
their *semantics* — multiple stateless PDP replicas, no sticky sessions, internal-only
Cerbos exposure, pod-local Admin API access — so the design's claims remain testable.

### The precedence boundary — the single most important constraint

Permission precedence is expressed **exclusively in Cerbos policies**. The ADS resolves and
supplies *data*: the sets of role-granted, user-granted and user-revoked actions for the
requested resource, plus the permission revision. It never computes a verdict.

Any Go code that decides "revoke beats grant" is a defect, regardless of whether it produces
the correct answer, because it creates the duplicated-logic failure mode §21 warns about.
An architecture test enforces that no library outside the Cerbos policy tree encodes
precedence ordering.

All rules evaluate against the single synthetic role `sys:permission-evaluator`, which the
ADS injects after token verification. Real IdP roles travel as principal attributes for
audit and optional conditions. A token presenting any `sys:`-prefixed role is rejected.

### Resource catalog and generation

- The complete concrete FHIR R6 resource list is used. Abstract types (`Resource`,
  `DomainResource`) are excluded; everything else concrete is included. The number 119 from
  the original brief is **not** treated as a contract — the committed manifest is the source
  of truth and makes the real count auditable.
- Six actions per resource: `create`, `read`, `update`, `delete`, `list`, `assign`. `assign`
  means transferring responsibility for a resource instance to a practitioner or team; it
  mutates an `assignee` attribute and is authorized independently of `update`.
- One generator consumes the manifest and emits: the resource-action catalog, one Cerbos
  resource policy per resource (honouring ADR-006), the principal and per-resource JSON
  schemas, the exhaustive Cerbos test suite, and the database catalog seed. All outputs are
  committed and golden-file tested, so a generator change produces a reviewable diff.

### UI capability catalog

- Exactly **400** capabilities, produced by instantiating five archetypes across eighty
  resources:
  `X.route.list` (`X:list` + `hospital_context:read`),
  `X.route.details` (`X:read` + related read),
  `X.route.edit` (`X:read` + `X:update` + related list),
  `X.button.assign` (`X:read` + `X:assign`),
  `X.action.delete` (`X:read` + `X:delete`).
- The five worked examples from §12.1 — including the nested `anyOf` in
  `patient.button.create-order` — are hand-authored so realistic nesting is represented.
- Archetypes deliberately share leaves across capabilities so that deduplication is
  genuinely exercised rather than trivially satisfied.
- Capabilities are versioned application configuration and are **not** tenant-editable.
  Administrators assign permissions only at the resource-action level.

### Capability evaluation contract

Given a set of capability keys and a routing context, the evaluator: loads definitions for
the active catalog revision → resolves each `targetRef` server-side into a real instance or
collection with trusted attributes → flattens expressions to leaves → deduplicates by
`(resource kind, resource id, action)` → resolves role grants and overrides once per
subject → issues a bounded number of batched Cerbos `CheckResources` calls → builds a leaf
decision map → evaluates every expression in memory → returns the snapshot with both
revisions, a context fingerprint and audience-filtered failure evidence.

`targetRef` is **never** trusted from the browser. Routing identifiers may be supplied; the
authorization attributes behind them are always server-loaded.

### Data model

The §8.1 tables are implemented as specified: `installation_config`,
`authorization_resource`, `authorization_action`, `ui_capability_definition`,
`role_permission`, `user_permission_override`, `permission_revision`,
`permission_audit_event`, `outbox_event`. Plus one table not in the design: a portable
polymorphic `fhir_resource` table (`resource_type`, `id`, `tenant_id`, `hospitalId`,
`status`, `department`, `sensitivity`, JSON payload) backing the Resource Service, so that
mandatory rules such as `status != "LOCKED"` have real state to evaluate against.

Portability rules: generic Liquibase types only, `dbms`-qualified changesets solely where
dialects genuinely diverge, no reliance on Postgres-specific JSON operators in application
queries, identifiers within Oracle's limits, and explicit handling of Oracle's
empty-string-is-null semantics. All §8.2 constraints and indexes are implemented, and every
matrix save uses an expected revision.

### Load model

| Dimension | Value |
|---|---|
| Tenants | 5 |
| Hospitals | 20 (4 per tenant) |
| Canonical roles | 250, installation-wide; `role_permission` scoped per tenant |
| Users | 600,000, evenly distributed |
| Roles per user | 70 (42,000,000 mappings) |
| Users with overrides | ~5% (~30,000), 1–10 each, ≈150,000 rows |
| Override composition | GRANT, REVOKE and expired, deliberately mixed |
| Concurrent VUs | 1,000, protocol-level |
| Token flow | One password grant per VU, then refresh-token rotation |

The demo profile uses the same generators with small parameters. Load and demo differ by
configuration only, never by code path.

### API surface

The §9.4 administration endpoints are implemented as specified. The runtime exposes
`POST /internal/authz/check` (Appendix B shape) and a capability evaluation endpoint
returning the §12.4 snapshot. The Resource Service exposes generic FHIR CRUD, list and
assign operations, each acting as its own policy enforcement point and returning
per-instance action decisions in the Appendix A shape.

## Testing Decisions

### What makes a good test here

Tests target **external behaviour through a module's public interface**. A test that asserts
which cache eviction strategy `authzcache` uses is a bad test; a test that asserts a
revoked permission stops being served within the convergence window is a good one. Tests
must survive a rewrite of the internals they cover.

Two rules follow from the domain:

- **Never assert precedence outcomes by calling Go code.** Precedence lives in Cerbos.
  Precedence tests are Cerbos policy tests. A Go test that "confirms" revoke beats grant is
  testing a duplicate implementation that should not exist.
- **Golden-file tests for generators.** Generated policies, schemas, capabilities and Cerbos
  test suites are committed. A generator change must surface as a reviewable diff, not as a
  silently different artifact.

### Prior art

None. This repository contains only a design document, so this PRD establishes the testing
conventions rather than following them. Conventions adopted: Go standard `testing` with
table-driven cases; `testcontainers-go` for anything needing a real engine; Cerbos' native
policy test format for all policy tests; Jest for Angular; k6 `thresholds` as the load-test
assertion mechanism.

### Coverage by layer

All ten layers of the §19 table are in scope.

**Deep library unit tests — public interface only.** `capabilityeval` is the highest-value
target: pure, zero-I/O, and where the novel composition logic lives — covering `allOf` and
`anyOf`, nesting, leaf deduplication, and failure-evidence shape. `capabilitycatalog` covers
every CI invariant: empty arrays, unknown resource, unknown action, circular reference,
negation attempt. `tokenverifier` covers issuer, audience, expiry and signature rejection
plus `sys:`-prefix rejection. `permissioncontext` covers validity windows, expired
assignments and instance selectors, asserting emitted *data* only. `cerbosclient` covers
request-limit chunking at and beyond the boundary. `authzcache` covers bounded eviction,
targeted invalidation and reconciliation. `cataloggen` is golden-file tested.

**Dual-dialect contract suite.** One dialect-agnostic suite for `assignmentstore`, executed
against both PostgreSQL and Oracle via testcontainers in CI. Same assertions, both engines,
no exceptions — this is the mechanism that makes the portability claim real.

**IdP contract suite.** One suite every `IdentityDirectory` implementation must pass:
pagination, canonical identifier format, unresolved role handling, provider-unavailable
behaviour. Run against Keycloak; the WSO2 adapter must pass the same suite when added.

**Cerbos policy tests — exhaustive.** The full seven-case §19.1 matrix for every
resource-action across the entire catalog (~6,600 cases), plus generated tenant-isolation,
hospital-isolation and default-deny invariants. Generated from the manifest, committed, and
run in a dedicated sharded CI job so it does not block fast feedback.

**Service integration tests.** ADS cache hit and miss; Kafka invalidation convergence;
administration optimistic concurrency; audit and outbox atomicity under rollback; tenant
boundary enforcement; backend fail-closed on PDP unavailability; policy release
validate-archive-activate-verify and rollback.

**Angular unit tests.** `CapabilityStore`, `capabilityGuard`, the structural directive,
stale-snapshot detection and refresh, and the 403 retry-once path.

**Performance tests.** The k6 suite is itself an assertion: §15.3 objectives as hard
thresholds, so a run exits non-zero on breach. Includes a mid-run permission mutation
scenario that measures convergence and revocation latency under load, and a token-endpoint
baseline scenario so identity cost stays attributable.

**Chaos tests.** Scripted scenarios asserting the §18 failure table: kill a PDP replica
(traffic continues, no sticky session); kill an ADS replica (caches warm on demand); pause
Kafka then resume (writes committed, reconciler converges); block the database (warm
decisions continue, misses fail closed); fail a policy rollout mid-flight (previous archive
stays active, release not marked active); stop Gitea (active revision keeps serving).

## Out of Scope

- **Kubernetes artifacts.** No Helm charts, HPA, PDB, topology spread, NetworkPolicy or mTLS
  configuration. Compose preserves the semantics; the manifests are a later exercise.
- **A working WSO2 adapter.** The port, the env-driven selection and the shared contract
  suite exist and prove neutrality. The SCIM2 implementation is not written.
- **Production identity lifecycle.** No password management, user provisioning,
  self-service, or MFA configuration. Keycloak is used as-is.
- **Maker-checker and governance workflow.** §9.2 mentions optional approval for high-risk
  changes; §20 Phase 6 defers governance. Not implemented.
- **Instance-specific overrides beyond schema support.** The optional
  `resource_instance_id` dimension exists in the schema and in `permissioncontext`, but no
  Admin Console screen manages it.
- **Cerbos scoped policies.** §5.3 explicitly recommends deferring these. Not used.
- **Cerbos query-plan adapter.** §15.2 suggests it for very large lists; the prototype uses
  batched checks with bounded page sizes instead.
- **Server-sent capability revision notifications.** §12.6 describes this as optional and
  explicitly not a security mechanism. Polling on revision change is used instead.
- **Real FHIR conformance.** Resources are FHIR-*named* and carry a JSON payload, but the
  prototype is not a conformant FHIR server and performs no profile validation.
- **Browser-based load testing.** Explicitly excluded; 1,000 headless browsers is not
  achievable on the target hardware.
- **Visual design.** Angular Material defaults. No design system, no responsive polish, no
  accessibility pass.
- **Production hardening.** Read-only root filesystems, seccomp profiles, dropped
  capabilities, secret managers and backup or point-in-time recovery procedures are
  described in the design but not implemented here.
- **Multi-installation deployment.** One installation, one identity provider, as the design
  specifies.

## Further Notes

**Known scale risks, and what each would tell us.** These are the points where the prototype
is most likely to fail, which is precisely why they are worth building:

- *Keycloak token throughput.* Even with refresh-token rotation, refresh-token rotation
  writes to Keycloak's database on every refresh. If the identity provider caps total
  throughput below what the ADS can serve, that is a genuine and useful finding — the
  token-endpoint baseline scenario exists to make it attributable rather than confusing.
- *ADS user-override cache.* 600,000 distinct users against a bounded cache will produce
  eviction and database read-through under a uniform access distribution. The realistic
  answer is a skewed access pattern in the load model, but the measured hit ratio is a real
  sizing output, not a number to tune until it looks good.
- *Exhaustive Cerbos suite.* ~6,600 generated test cases across ~157 policy files may take a
  long time to compile and run. If it does, that is itself a finding about ADR-006 at full
  catalog scale, and the mitigation is sharding, not reducing coverage.
- *Oracle in CI.* The Oracle 23ai Free image is large and slow to start. The dual-dialect
  job will be the slowest in CI and should run on merge rather than on every push.
- *Compose replica semantics.* Compose has no leader election. The Policy Controller's
  leader election is implemented against a database advisory lock so the mechanism is real
  and portable to Kubernetes.

**Deliberate departures from the source design**, all recorded above and each with a stated
reason: Go instead of Java; the IdP adapter as a library rather than a service; Kubernetes
manifests omitted; the additional `fhir_resource` table; and the abandonment of "119" as a
resource-count contract in favour of the complete R6 list.

**Traceability.** Every implementation slice derived from this PRD cites the design section
it implements. Where a slice deviates from v1.3, the deviation and its reason are stated in
the issue rather than discovered later in the code.
