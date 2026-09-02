# ADR-010: A tenant is a Keycloak realm, a hospital is a Keycloak organization

The platform was built on the belief that one realm holds every tenant and
that `tenant_id`/`hospital_id` arrive as ordinary user-attribute claims a
realm administrator can edit. That is not how the installation is actually
structured, and the misunderstanding put tenant isolation on the wrong
foundation: an attribute rather than a cryptographic boundary. Issue #75's
spike and issue #78 onward correct it at the source.

## The decision

**A tenant is a Keycloak realm.** Each tenant has its own issuer, its own
signing keys, its own users and its own roles. `tenantId` is the realm name
verbatim - there is no realm-to-tenant mapping layer, because a mapping
would be a second source of truth alongside the realm name already embedded
in every canonical role identifier (`kc:<realm>:...`, §7.5). The tenant a
decision is made for is the realm that verifiably signed the token
(`libs/tokenverifier`), never a claim inside it.

**A hospital is a Keycloak organization inside that realm.** `hospitalId`
is the organization's **alias**. A user can belong to many organizations at
once; a session names exactly one active hospital - the organization
Keycloak confirmed the user is a member of and the user selected during
login (ADR-012) - or none, which means tenant-wide and is reachable only by
an administrator's explicit choice.

## Why this, not a claim

The property being bought is that isolation rests on something the
platform did not have to trust the token to tell it honestly. A realm's
identity is the issuer that signed the JWT and the keys the platform
fetched from that realm's own JWKS endpoint - forging it means forging a
signature, not editing an attribute. An organization membership is
something Keycloak itself attests by populating the `organization` claim
only for organizations it confirmed the user belongs to (measured directly:
requesting `scope=organization:<alias>` for an organization the user does
not belong to is silently dropped, never granted -
`docs/MEASURED_FINDINGS.md`). Neither fact is something a realm
administrator, a browser, or a request body can rewrite.

## Consequences

- **The design document's §7.1 `tenantMappingMode` contract is not
  implemented.** §7.1 imagines an installation-level choice of how tenant
  is derived; this platform removed that axis of configuration entirely,
  because the one mapping that satisfies the isolation property above -
  realm as tenant - is not a choice to leave open. Recorded as DEVIATIONS
  S8, with this ADR as the reasoning.
- **The organization alias is the hospital identifier everywhere**:
  policies, `permissionContext`, seed data, and the k6 load model. Renaming
  an organization's alias in Keycloak silently orphans every assignment row
  keyed on the old alias - there is no migration path, because Keycloak is
  the sole system of record for organizations and this platform is never
  told about a rename. Accepted as a known limitation for a prototype; a
  production deployment would need either a stable internal id surfaced
  alongside the alias, or a documented rename procedure that re-keys
  assignment data.
- One deployment serves every tenant by trusting a declared set of realms
  rather than one hardcoded realm (ADR-011), and a session's hospital comes
  from an in-flow selection over real memberships, never a request
  parameter (ADR-012).
- `libs/idpdirectory`'s organization reads (`OrganizationsOfTenant`,
  `OrganizationsOfUser`, `MembersOfOrganization`) are read-only by
  construction - `tests/architecture/organizationwrites.go` forbids any
  write path - because Keycloak already is the system of record and a
  second one would disagree with it eventually.
