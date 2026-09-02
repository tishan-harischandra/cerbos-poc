# ADR-011: The tenant registry is declared trust, then a database of record

Once a tenant is a realm (ADR-010), one deployment serving several tenants
needs to know which realms it trusts, and needs to learn about a new one
without redeploying every service. Two different concerns were at risk of
being conflated into one: "who do we trust" is a governance question that
should require a reviewed change, and "which tenants exist right now" is an
operational fact that changes at runtime.

## The decision

**The set of trusted realms starts as a file, ends as a table.**
`deploy/tenant-registry.yaml` lists every tenant this installation trusts
at deploy time - realm, issuer, browser client id, service client id, and a
reference to where its service-account credential is mounted, never the
credential itself. `libs/tenantregistry` parses and validates it;
`libs/assignmentstore/tenantseed` seeds it into the `tenant_registry` table
idempotently on every service start, the same table on both PostgreSQL and
Oracle (§8, the dual-dialect constraint every other table already carries).

**After seeding, the database is the record of authority, not the file.**
`apps/admin-service/internal/tenantonboarding` exposes onboarding a new
tenant through the Administration Service - a write to `tenant_registry`,
not a config change and not a restart. Every service resolves a token's
realm against this table, live, so a newly onboarded tenant becomes usable
immediately (`apps/ads/internal/tenantdiscovery` polls it), and the trusted
set can only grow through an API call that is itself audited, never through
editing a file on a running host.

**An unregistered realm is refused outright**, which is what makes the
existing foreign-issuer negative test (issue #77) meaningful rather than
incidental: `libs/tokenverifier` selects a registry entry by the token's
issuer, and there is no entry for a realm nobody trusted.

## Why file-seeded rather than either extreme

A registry that lived only in a file would need a redeploy to onboard a
tenant, defeating the point of one deployment serving all of them (issue
#86's user story). A registry that started life in the database with
nothing to seed it from would mean the very first trust decision - which
realms an operator is willing to stand behind before the platform has ever
run - has no reviewable artifact: a database row someone inserted by hand
leaves no diff, no reviewer, no commit message. The file is the initial,
reviewed grant of trust; the table is what lets trust grow afterward without
that review gate blocking every subsequent tenant. This is a shape already
proven elsewhere in this codebase - the Cerbos root policy release is also
"reviewed artifact in, database record of what's active" (§13) - reused
here rather than invented.

## Consequences

- **Onboarding trusts any authenticated caller** (issue #86,
  `docs/MEASURED_FINDINGS.md`): the onboarding endpoint checks that the
  caller holds a valid token for *some* registered tenant, not that they
  hold a platform-operator credential this prototype has no design for.
  Accepted as a known gap for a prototype whose realms are all created by
  the same operator; a production deployment would need a distinct
  platform-operator role before this endpoint is reachable by anyone but
  that role.
- Registry changes propagate to running replicas through the tenant
  registry table's own polling loop, not a second cache-invalidation
  mechanism layered on top of the existing revision/event machinery -
  because a *third* invalidation path in a system that already has one for
  permissions (§10) and one for policy releases (§13) would be one too
  many things that can disagree with each other.
- The credential itself is never in the registry file, the table, or any
  response the browser can reach - only a reference to where it is mounted.
  `identity-e2e.sh` asserts this directly for the existing two-tenant
  registry, and the same assertion covers a tenant onboarded at runtime.
- A malformed or duplicate entry in the seed file fails fast at startup,
  before any service accepts a request, rather than surfacing as an
  intermittent per-realm lookup failure later.
