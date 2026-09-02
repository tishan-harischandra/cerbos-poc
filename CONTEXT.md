# Multi-tenant authorization platform

A dedicated Cerbos-based authorization runtime for a multi-tenant, multi-hospital
healthcare installation: root policy logic lives in versioned Cerbos files, while
role and user assignments live in a database and reach the decision path as data.

## Language

### Tenancy and identity

**Installation**:
One deployment of the platform, with exactly one active identity provider, serving every trusted **Tenant**.

**Tenant**:
A Keycloak realm, and the top-level isolation boundary that assignment data is partitioned by. `tenantId` is the realm name verbatim (ADR-010) — the tenant a decision is made for is the realm that verifiably signed the token, never a claim inside it.

**Hospital**:
A Keycloak organization inside a **Tenant**'s realm, and a sub-scope within it that an assignment or override may be narrowed to. `hospitalId` is the organization's alias. A user may belong to **many** organizations at once; a session has exactly one active hospital, taken from the organization the user selected during login (ADR-012), or none — which means tenant-wide, and is reachable only by an administrator's explicit choice.
_Avoid_: implying a hospital is a free-form attribute — it is a real membership Keycloak confirmed, not a claim the platform trusts on its own.

**Tenant Registry**:
The declared set of realms this installation trusts — file-seeded, then a database of record (ADR-011). A token from a realm outside it is refused outright.

**Identity Directory**:
The provider-neutral port for reading users and roles from the installation's identity provider.
_Avoid_: IdP client, Keycloak client

**Canonical Role ID**:
The normalised role identifier the **Role Matrix** is keyed by, byte-identical whether derived from a token or from the **Identity Directory**.

### Permissions

**Role Matrix**:
The tenant-scoped mapping from a role to the resource-actions it permits.
_Avoid_: role permissions table, RBAC matrix

**User Override**:
A user-specific `INHERIT`, `GRANT` or `REVOKE` on one resource-action, optionally bounded by a validity window.
_Avoid_: exception, user permission

**Permission Revision**:
The monotonic per-tenant counter bumped by every assignment write, used to tell which state a decision was taken against.

**permissionContext**:
The assembled data structure the **ADS** attaches to a Cerbos request. It carries facts only — it never contains a verdict.

**Precedence**:
The ordering `mandatory deny > user REVOKE > user GRANT > role grant > default deny`. It exists **only** inside Cerbos policy, via the synthetic `sys:permission-evaluator` role (ADR-003). Go code that encodes this ordering is a defect.

### Policy and catalog

**Resource Catalog**:
The active list of resource kinds and their permitted actions, released alongside the policies.

**Root Policy Release**:
An immutable, validated archive of Cerbos policy files and catalog, produced from a Gitea tag and activated across the PDP fleet.

**Capability**:
A named composite of permission leaves that one UI route, component or control depends on.
_Avoid_: permission, feature flag

**Capability Snapshot**:
The evaluated set of **Capability** results returned to the browser for a module or a loaded page context.

### Runtime components

**PDP**:
The Cerbos deployment that evaluates policy and returns decisions.

**ADS**:
The service that resolves assignments, assembles **permissionContext**, calls the **PDP** and serves **Capability Snapshots**.

**PEP**:
Any backend that enforces a decision by asking the **ADS** before acting. The Resource Service is one.

**Administration Service**:
The write path for the **Role Matrix** and **User Overrides**, and the BFF that serves the **Admin Console** (ADR-008).

**Admin Console**:
The Angular application administrators use. It is an asset surface of the **Administration Service**, not a separately deployed workload.

### Coordination

**Election**:
A named singleton role that at most one replica holds at a time, e.g. `outbox-publisher` or `policy-controller`.
_Avoid_: lock, lease, mutex — those name mechanisms, not the role

**Elector**:
The port a workload uses to run work only while it holds an **Election** (ADR-009). Its adapters are selected by `LEADER_ELECTION_TYPE`.

**Outbox Publisher**:
The **Election** that drains committed outbox rows to Kafka.

## Relationships

- A **Tenant** contains many **Hospitals**; assignment data is partitioned by both
- A user may belong to many **Hospitals** within one **Tenant**, but a session's active hospital is exactly one, or none
- A **Role Matrix** entry and a **User Override** both resolve to resource-action leaves; a **User Override** wins under **Precedence**
- The **ADS** assembles **permissionContext** and the **PDP** applies **Precedence** to it
- One **Capability** composes many permission leaves; one leaf contributes to many **Capabilities**
- A **Root Policy Release** version-stamps the **Resource Catalog** and the policy files together
- Each **Election** is held by at most one replica; the **Elector** port has one active adapter per **Installation**

## Example dialogue

> **Dev:** "If a user has a **User Override** of `REVOKE` on `patient_record:read` but their role grants it, where does the deny happen?"
>
> **Domain expert:** "In the **PDP**. The **ADS** just puts both facts in **permissionContext** — the moment the **ADS** decides which one wins, we've moved **Precedence** out of policy and the guarantee is gone."
>
> **Dev:** "And if the route is hidden in the **Admin Console** because the **Capability** evaluated false, do I still need the check on the write?"
>
> **Domain expert:** "Always. A **Capability Snapshot** is a rendering hint. The **PEP** enforces."

## Flagged ambiguities

- **"ADS"** expanded to *Authorization Decision Service* in
  `docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md` §1.1 but to *Assignment
  Data Service* in `apps/ads/internal/server/server.go`. Resolved: the design
  document's *Authorization Decision Service* is authoritative, and the code
  comment now says so.
- **"admin"** was used for both the **Administration Service** and the **Admin
  Console**. Resolved: these are distinct concepts, and remain so as terms even
  though ADR-008 makes them one deployment.
- **"lock"** was used for both the mechanism and the singleton role. Resolved:
  the role is an **Election**; a lock, lease or advisory lock is one adapter's
  mechanism for holding one.
- **"Tenant" and "Hospital"** were originally understood as an editable user
  attribute claim (`tenant_id`/`hospital_id`) — a misunderstanding the
  platform was actually built on for a time. Resolved (ADR-010): a **Tenant**
  is a realm and a **Hospital** is an organization, and neither is trusted
  from a claim the platform did not itself verify against the identity
  provider's own structure.
