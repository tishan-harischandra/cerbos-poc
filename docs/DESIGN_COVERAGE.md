# Design coverage, §5 through §19

Every section of
[`Cerbos_Multi_Tenant_Authorization_Design_v1.3.md`](Cerbos_Multi_Tenant_Authorization_Design_v1.3.md)
from §5 to §19 is either implemented and cited here, or listed as out of scope
with a reason. Deviations are recorded separately in
[`DEVIATIONS.md`](DEVIATIONS.md); this table answers the prior question of
whether a section was built at all.

Status is one of **implemented**, **partial** (built, with a named narrowing)
or **out of scope**.

## §5 Multi-tenant and hospital context model

| Section | Status | Where |
|---|---|---|
| §5.1 Hierarchy | implemented | `libs/assignmentstore/store.go` (tenant/hospital keys), `deploy/liquibase/changelog` |
| §5.2 Trusted decision attributes | implemented, differently | `apps/ads/internal/tokenauth`, `libs/tokenverifier` - tenant comes from the verified realm and hospital from a confirmed organization membership, never an editable claim (ADR-010) |
| §5.3 Scope usage | out of scope | §5.3 itself recommends deferring per-tenant scoped policies (DEVIATIONS N1) |

## §6 Authorization domain model

| Section | Status | Where |
|---|---|---|
| §6.1 Resource, action and UI-capability catalogs | implemented | `libs/cataloggen`, `libs/capabilitycatalog`, `deploy/cerbos/catalog` - 156 resource policies, six actions each, 400 capabilities |
| §6.2 Permission assignment types | partial | `role_permission`, `user_permission_override` including the instance-scoped dimension; no console screen for the instance case (DEVIATIONS N4) |
| §6.3 Permission precedence | implemented | `deploy/cerbos/policies/resources/*.yaml` only. Never in Go - enforced by `tests/architecture/precedence_test.go` |
| §6.4 One synthetic role | implemented | `sys:permission-evaluator` injected by the ADS; a token presenting any `sys:` role is refused (`identity-e2e.sh`) |
| §6.5 Root resource-policy pattern | implemented | The generated policies follow the pattern exactly; request schemas in `deploy/cerbos/policies/_schemas` |

## §7 Identity provider integration

| Section | Status | Where |
|---|---|---|
| §7.1 Installation selection | implemented, differently | `IDP_TYPE` and `libs/idpdirectory/provider` select the provider; `tenantMappingMode` itself is not implemented at all - a tenant is always the realm (ADR-010, DEVIATIONS S8) |
| §7.2 Adapter interface | implemented | `libs/idpdirectory/port.go` (Go, not Java - DEVIATIONS S1) |
| §7.3 Keycloak adapter | implemented | `libs/idpdirectory/keycloak`, exercised against a real Keycloak by `identity-e2e.sh`; read-only organization/membership access (ADR-010) and the in-flow login authenticator (`apps/keycloak-org-selector`, ADR-012) are additions the design does not describe |
| §7.4 WSO2 adapter | partial | Port and contract only; SCIM2 calls not written (DEVIATIONS N2) |
| §7.5 Canonical identifiers | implemented | `libs/canonicalid`, asserted byte-for-byte against token normalisation in `identity-e2e.sh` |

## §8 Authorization data model

| Section | Status | Where |
|---|---|---|
| §8.1 Tables | implemented, plus one | `deploy/liquibase/changelog`; the extra `fhir_resource` table is DEVIATIONS A1 |
| §8.2 Constraints and indexes | implemented | Same changelog set, applied to PostgreSQL and Oracle by `make db-test-dual` |
| §8.3 Override semantics | implemented | `libs/assignmentstore`, `libs/permissioncontext` - a disabled role row is not a denial |

## §9 Administration console

| Section | Status | Where |
|---|---|---|
| §9.1 Modules | implemented | `apps/admin-console/src/app` - role matrix, user overrides, resource catalog, simulator, audit search, revision activation, IdP diagnostics |
| §9.2 Screen behaviour | partial | Impact preview, expected-revision conflict handling and unresolved-role flagging are implemented; maker-checker is not (DEVIATIONS N3) |
| §9.3 Console deployment | implemented, differently | Served by the Administration Service (ADR-008, DEVIATIONS S3) |
| §9.4 Administration API | implemented | `apps/admin-service/internal/{rolematrix,useroverride,catalogapi,simulate,auditsearch,platformstatus}` |

## §10 Permission update and cache convergence

| Section | Status | Where |
|---|---|---|
| §10.1 Atomic write | implemented | `SaveRoleMatrix` writes permission, audit, outbox and revision in one transaction |
| §10.2 Event shape | implemented | `libs/permissionevents` - one `PermissionChanged` per touched resource-action |
| §10.3 Convergence and repair | implemented | `apps/ads/internal/invalidation` - Kafka consumer plus a revision reconciler that repairs whatever the transport loses |

## §11 Runtime authorization flow

| Section | Status | Where |
|---|---|---|
| §11.1 Decision endpoint | implemented | `POST /internal/authz/check`, Appendix B shape |
| §11.2 Warm path and caches | implemented | `apps/ads/internal/assignments/cache.go`, hit ratio exported per cache |
| §11.3 Enforcement points | implemented | `apps/resource-service` is a PEP for generic FHIR CRUD, list and assign |

## §12 Angular CSR composite-capability rendering

| Section | Status | Where |
|---|---|---|
| §12.1 Capability catalog | implemented | Five worked examples hand-authored, 395 generated (DEVIATIONS S6) |
| §12.2 Composite evaluation | implemented | `libs/capabilityeval` - pure, and kept pure by `tests/architecture/purity_test.go` |
| §12.3 Evaluation algorithm | implemented | `apps/ads/internal/capability` - server-side target resolution (DEVIATIONS A4), leaf deduplication, bounded batching |
| §12.4 Snapshot shape | implemented | Both revisions, context fingerprint, audience-filtered failure evidence |
| §12.5 Rendering | implemented | `libs/web/capability` guard and store, used by `apps/business-ui` |
| §12.6 Refresh | partial | Snapshot reload on revision change; no server-sent notification, which §12.6 makes optional (DEVIATIONS S5) |

## §13 Root policy lifecycle

| Section | Status | Where |
|---|---|---|
| §13.1 Release pipeline | implemented | `apps/policy-controller`, `libs/policyrelease` - Gitea tag, compile, atomic install, activation |
| §13.2 Verification across replicas | implemented | Release status and per-replica activation surfaced by `GET /admin/authz/policy-releases` |

## §14 Kubernetes deployment

| Section | Status | Where |
|---|---|---|
| Workloads, services, autoscaling | implemented | `deploy/k8s` kustomize base and four overlays; KEDA `ScaledObject` per scalable service; `make k8s-validate` |
| Leader election | implemented, differently | A vendor-neutral port with five adapters (ADR-009, DEVIATIONS S4) |
| NetworkPolicy, mTLS, PDB, topology spread | out of scope | DEVIATIONS N5; the README names them as remaining manual steps |

## §15 Performance and scalability

| Section | Status | Where |
|---|---|---|
| §15.1 Load model | implemented | `libs/loadmodel`, `libs/keycloakbulkload` - 5 tenants, 20 hospitals, 250 roles, 600,000 users, 42M mappings |
| §15.2 Batching and caching | implemented | Bounded batched `CheckResources` rather than the query plan (DEVIATIONS S7) |
| §15.3 Objectives | implemented as thresholds | `deploy/loadtest/k6` - warm decision latency, convergence and fail-closed are k6 thresholds, so a breach exits non-zero. See [`LOAD_TESTING.md`](LOAD_TESTING.md) |

## §16 Security design

| Section | Status | Where |
|---|---|---|
| §16.1 Network posture | partial | Nothing but the two browser entry points is published in compose; ClusterIP only in Kubernetes. No NetworkPolicy or mTLS objects (DEVIATIONS N5) |
| §16.2 Token handling | implemented | `libs/tokenverifier` - issuer, audience, signature and expiry, with a real JWKS |
| §16.3 Credential handling | implemented | The IdP service-account secret is file-mounted, never an environment variable; `identity-e2e.sh` asserts no browser-reachable response carries it |
| §16.4 Audit | implemented | `permission_audit_event` on every write, searchable from the console |

## §17 Observability and audit

| Section | Status | Where |
|---|---|---|
| §17.1 Metrics | implemented | `apps/ads/internal/{adsmetrics,invalidationmetrics,revisionmetrics}`, scraped by the `observability` compose profile |
| §17.2 Audit trail | implemented | §16.4 above, plus the correlation id carried from the console through the write |

## §18 Availability, failure modes and disaster recovery

| Section | Status | Where |
|---|---|---|
| Failure-mode table | implemented as tests | `scripts/chaos/scenarios` - PDP loss, ADS loss, database outage, Kafka outage, policy rollout failure, IdP outage, node drain, run against a real kind cluster by `make chaos` |
| Backup and point-in-time recovery | out of scope | Operational procedure, not a property this prototype can demonstrate |

## §19 Testing strategy

| Layer | Status | Where |
|---|---|---|
| §19.1 Exhaustive policy matrix | implemented | 11,303 assertions across 156 resource policies, `make policy-test` |
| Unit and contract | implemented | `nx run-many --target=test`, plus the dual-dialect store and leader-election contracts |
| Integration and end to end | implemented | `make smoke` - stack, identity and decision suites against a running stack |
| Deployment | implemented | `make k8s-validate`, `python3 scripts/tests/compose-contract.py` |
| Architecture | implemented | `make arch-test` - four invariants, each with a deliberate-violation test |
| Load | implemented | `make loadtest` ([`LOAD_TESTING.md`](LOAD_TESTING.md)) |
| Chaos | implemented | `make chaos` |
| Walkthrough | implemented | `make walkthrough`, and the same script against Kubernetes via `scripts/k8s-walkthrough.sh` |
