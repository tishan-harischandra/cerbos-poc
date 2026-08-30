# PRD — A tenant is a realm, a hospital is an organization

## Problem Statement

The platform was built on the belief that one Keycloak realm holds every tenant,
and that tenant and hospital arrive as ordinary user attribute claims
(`tenant_id`, `hospital_id`). That is not how the installation is actually
structured.

In reality:

- A **tenant** is a Keycloak **realm**. Each tenant is a separate realm with its
  own issuer, its own signing keys, its own users and its own roles.
- A **hospital** is a Keycloak **organization** inside that realm.
- A user can be a member of **many** organizations at once.
- A login is **organization scoped**: a session is always for one hospital, and
  the user says which one while logging in.

Because the implementation assumed otherwise, everything downstream is wrong in
the same way. The identity configuration binds each service to exactly one realm
and one tenant id, and refuses any other tenant outright. Tenant is read from a
claim that a realm administrator controls, so tenant isolation rests on an
attribute rather than on a cryptographic boundary. Hospital is a free-form claim
with no relationship to any real membership, so nothing prevents a token
claiming a hospital the user has never worked at. A user who genuinely works at
two hospitals cannot be represented at all, and there is no moment in the login
where the user says which hospital they are working as today.

From the operator's point of view: five tenants cannot be served, staff with two
hospitals cannot log in correctly, and the isolation the whole prototype exists
to demonstrate is nominal.

## Solution

Make the identity provider's own structure the source of truth for both scopes.

A user reaches their tenant by hostname — one subdomain per tenant — and is sent
to that tenant's realm. They authenticate there. If they are a member of more
than one organization, Keycloak itself asks which hospital they are working as,
inside the login flow, before any token is issued. The resulting token names one
tenant (the realm that signed it) and one hospital (the organization selected),
and the platform trusts neither of them from a claim it cannot verify: the tenant
is the realm of the verified issuer, and the hospital is an organization
Keycloak has confirmed the user belongs to.

A user who belongs to no organization cannot log in. An administrator is offered
an extra "tenant-wide" choice on that same selection screen, which produces a
session with no hospital — and a session with no hospital can only ever reach
tenant-wide assignments, never a hospital-narrowed one.

Switching hospital during the day is a first-class action: the application asks
Keycloak for a new token scoped to the other organization, silently, with no
re-entry of credentials, and discards the old one. There is no notion of "the
hospital I asked for in this request" — the hospital is a property of the token.

One deployment serves all tenants. The set of realms it trusts is declared, not
discovered: a registry seeded from a reviewable file into the database, from
which every service resolves a realm to its issuer, keys and clients. A token
from a realm outside the registry is refused, which is what keeps the existing
foreign-issuer negative test meaningful.

## User Stories

1. As a clinician, I want to reach my hospital group's login page by its own web
   address, so that I never have to know which realm I belong to.
2. As a clinician who works at two hospitals, I want to choose which hospital I
   am working as while I log in, so that my permissions match where I actually am
   today.
3. As a clinician who works at one hospital, I want that hospital selected for me
   automatically, so that I am not asked a question with one answer.
4. As a clinician, I want to switch hospital without typing my password again, so
   that covering a shift elsewhere costs me one click.
5. As a clinician, I want my old hospital's session to stop working the moment I
   switch, so that I cannot act at a hospital I am no longer viewing.
6. As a clinician, I want to see which hospital my session is for at all times,
   so that I never write into the wrong hospital's record by accident.
7. As a member of staff with no hospital membership, I want to be told clearly
   that my account is not attached to a hospital, so that I contact the right
   person instead of guessing at my password.
8. As an administrator, I want to log in without being forced to pick a hospital,
   so that I can administer the whole tenant.
9. As an administrator, I want an explicit tenant-wide choice on the selection
   screen, so that operating across hospitals is a deliberate act and appears as
   such in the audit trail.
10. As an administrator who is also a clinician, I want to be able to choose a
    specific hospital instead of tenant-wide, so that I can do clinical work
    under exactly the scope a clinician would have.
11. As an administrator, I want to move between the platform's Admin Console and
    Keycloak's own administration console without logging in again, so that
    identity administration and authorization administration feel like one job.
12. As an administrator, I want a deep link into either console to survive login,
    so that a bookmark or a link in a ticket lands where it pointed.
13. As an administrator, I want to see the organizations of my tenant and their
    members in the Admin Console, so that I can understand an assignment's scope
    without leaving the tool.
14. As an administrator, I want organizations and memberships to be editable only
    in Keycloak, so that there is exactly one system of record for who works
    where.
15. As an administrator, I want to see which hospitals a user belongs to when I
    grant or revoke a permission, so that I understand the reach of what I am
    about to do.
16. As a platform operator, I want one deployment to serve every tenant, so that
    I do not run and upgrade five parallel stacks.
17. As a platform operator, I want the list of trusted realms to be a reviewed
    file in version control, so that adding a trusted issuer is a change someone
    approved.
18. As a platform operator, I want the registry to be the database of record once
    seeded, so that onboarding a tenant does not require redeploying every
    service.
19. As a platform operator, I want to onboard a new tenant through the Admin
    Service, so that a new hospital group can be brought up without a release.
20. As a platform operator, I want a token from a realm that is not registered to
    be refused, so that creating a realm in Keycloak does not silently grant
    access to the platform.
21. As a platform operator, I want each tenant's service-account credentials held
      separately, so that a leaked credential exposes one tenant rather than all
      of them.
22. As a security reviewer, I want the tenant to come from the realm that signed
    the token, so that tenant isolation rests on a signature rather than on an
    editable attribute.
23. As a security reviewer, I want the hospital to come from an organization
    membership Keycloak has confirmed, so that no token can name a hospital the
    user does not work at.
24. As a security reviewer, I want a token with no organization and no tenant-wide
    marker to be refused by the decision service, so that an unscoped token is
    never usable.
25. As a security reviewer, I want the list of a user's other organizations to be
    display data only, so that it cannot widen a decision.
26. As a security reviewer, I want a structural guarantee that no code derives the
    tenant or hospital from a request body, header or query parameter, so that
    the guarantee does not depend on review vigilance.
27. As a security reviewer, I want a tenant-wide session to be unable to reach a
    hospital-narrowed assignment, so that "no hospital" can never mean "every
    hospital".
28. As a security reviewer, I want cross-tenant access attempts to be rejected
    and logged distinguishably, so that they are alertable rather than lost among
    expired tokens.
29. As a developer, I want a single module that turns a request into a tenant, so
    that changing from subdomains to any other scheme touches one place.
30. As a developer, I want a single module that resolves a realm to its
    verification parameters, so that no service assembles an issuer URL by string
    concatenation.
31. As a developer, I want the organization selection logic to be a pure function
    of memberships and admin status, so that I can test every case without
    starting Keycloak.
32. As a developer, I want the identity adapter to declare how it derives tenant
    and hospital, so that a non-Keycloak provider can be supported without a
    global mapping mode.
33. As a developer, I want the configuration that allowed tenant to come from a
    claim removed entirely, so that the original misunderstanding cannot be
    reintroduced by an environment variable.
34. As a developer, I want the Java provider built inside a container, so that no
    JVM or Maven is required on my machine or in any Go service image.
35. As a developer, I want the login flow exercised by the existing curl-based
    e2e style, so that the suite gains no new toolchain.
36. As a test engineer, I want a test proving a user in two organizations gets a
    different hospital in the token depending on their selection, so that
    multi-membership is verified rather than assumed.
37. As a test engineer, I want a test proving a user cannot obtain a token scoped
    to an organization they do not belong to, so that the scope request cannot be
    forged.
38. As a test engineer, I want a test proving a valid token from tenant A is
    refused when used against tenant B's data, so that isolation is demonstrated
    rather than described.
39. As a test engineer, I want the identity directory contract suite to cover the
    organization reads, so that any future adapter must implement them the same
    way.
40. As a test engineer, I want the exhaustive policy matrix extended with the
    tenant-wide case and complemented by focused adversarial negatives, so that
    both breadth and the dangerous edges are covered.
41. As a load engineer, I want protocol-level virtual users to obtain
    organization-scoped tokens without a browser, so that the 1000-VU model still
    represents real sessions.
42. As a load engineer, I want the seeded population spread across five realms
    with multi-organization membership as the norm, so that the load test
    exercises the real shape of the data.
43. As a load engineer, I want to know the cost of seeding five realms before the
    run, so that a bulk load does not fail halfway through.
44. As a reviewer of the design, I want the deviation from design v1.3 §7.1
    recorded with its reasoning, so that the document and the code disagree
    knowingly rather than accidentally.
45. As a newcomer to the codebase, I want the glossary to say plainly that a
    tenant is a realm and a hospital is an organization, so that I do not repeat
    this misunderstanding.

## Implementation Decisions

**Structural mapping**

- `tenantId` is the realm name verbatim. Realms are named after tenants. There is
  no realm-to-tenant mapping layer, because a mapping would be a second source of
  truth alongside the realm already embedded in every canonical role identifier.
- `hospitalId` is the organization **alias**. Aliases appear in the organization
  claim and in the scope request, and keep policies, tests and seed data legible.
  Renaming an alias would orphan assignment rows; recorded as a known limitation.
- A session has exactly one active hospital, or none. None means tenant-wide and
  is reachable only by an administrator's explicit choice.

**Tenant resolution**

- Tenant is resolved from the request host: one subdomain per tenant. Resolution
  lives in a single module so the strategy is swappable; the host strategy is the
  default. Locally, wildcard DNS that resolves to the loopback address avoids
  host-file editing; in Kubernetes, a wildcard Ingress host.

**Tenant registry**

- A declarative registry file lists every tenant: realm, issuer, browser client
  id, service client id and credential secret reference. It is validated on load
  and seeds a registry table idempotently.
- After seeding, the database is the record of authority. The Admin Service
  exposes tenant onboarding. Registry changes propagate to running replicas
  through the existing revision and event machinery rather than a second
  invalidation mechanism.
- The registry table ships as a dual-dialect Liquibase changelog, running on both
  Postgres and Oracle like every other table.
- The trusted-issuer set is exactly the registry. An unregistered realm is
  refused, which is what the existing foreign-issuer realm now tests.

**Token verification**

- Verification becomes multi-issuer: the issuer in the token selects the registry
  entry, and the entry supplies the expected issuer, audience and JWKS endpoint.
  Keys are cached per realm.
- `TenantMappingMode`, the single-tenant configuration and the tenant and
  hospital claim-name configuration are removed. Each identity adapter instead
  declares an identity mapping: Keycloak's derives tenant from the verified
  realm and hospital from the organization claim.
- A verified token exposes the active hospital and, separately, the user's other
  memberships. The memberships are display data. An architecture test forbids
  reading them anywhere in a decision path.
- A token carrying neither an active organization nor the tenant-wide marker is
  refused by the decision service.

**Login flow**

- A Keycloak authenticator provider performs organization selection inside the
  login flow, after credentials and any second factor.
- Its behaviour is a pure decision over the user's memberships, whether the user
  holds the realm role named `admin`, and any organization alias already
  requested in the scope:
  - an alias requested in the scope and matching a membership — selected, no
    screen;
  - exactly one membership and not an administrator — auto-selected, no screen;
  - more than one membership — the selection screen;
  - administrator — the selection screen, with an additional tenant-wide entry;
  - no membership and not an administrator — login refused with an explicit
    reason.
- The provider never overrides `redirect_uri`. Both Keycloak's own
  administration console and the platform Admin Console are legitimate
  destinations, and a deep link into either survives login untouched.
- Because the Keycloak SSO session is shared across clients in a realm, moving
  between the two consoles is a plain navigation. The Admin Console gains a link
  to Keycloak's console, and the login theme gains a link back.
- Hospital switching is a fresh authorization request naming the target alias,
  made silently against the existing SSO session. The previous token is
  discarded.

**Identity directory**

- The directory is keyed per tenant rather than bound to one realm, with a
  service-account client and secret per tenant. Cross-tenant lookups are an
  error, not a filtered result.
- The port gains organization reads: the organizations of a tenant, the
  organizations of a user, and the members of an organization. Organizations and
  memberships are never written through this platform — Keycloak is the sole
  system of record.

**Decision path**

- The principal presented to the policy decision point carries the tenant from
  the verified realm and the hospital from the selected organization. Nothing in
  the request body, headers or query string can influence either.
- Policies gain the tenant-wide case. The invariant is directional: an empty
  hospital matches tenant-wide assignments and never a hospital-narrowed one. No
  precedence logic moves out of the policies — the decision service continues to
  assemble facts only.

**Applications**

- Both front ends resolve their tenant from the host, obtain their OIDC issuer
  and client for that tenant, display the active hospital prominently, and offer
  a switcher driven by the memberships in the token. Shared resolution and
  silent re-authentication live in the shared web library.

**Data and seeding**

- Tenant and hospital column semantics are unchanged; their values change.
  Existing seed and demo data is regenerated rather than migrated — this is a
  prototype with no production data.
- The load and demo model becomes five realms of roughly 120,000 users each,
  four organizations per realm, each user a member of two of the four, 250 roles
  per realm and the existing override proportion. Multi-organization membership
  is therefore the common case in the data, not an edge case.
- Protocol-level load testing obtains organization-scoped tokens through the
  direct grant with an organization scope. This is validated by a short spike
  before dependent work begins, because if Keycloak declines to populate the
  organization claim outside the browser flow, the load model needs rethinking.

**Build and packaging**

- The Keycloak image is built from a Dockerfile with a Maven stage for the
  provider and a Keycloak build stage. No JVM or Maven on the host; no JVM in any
  Go service image.
- Compose and Kubernetes both gain five realms, wildcard hostnames, per-tenant
  secrets, the registry file and the custom Keycloak image.

**Documentation**

- Architecture decision records for: a tenant is a realm and a hospital is an
  organization; the tenant registry as declared trust with a database of record;
  organization-scoped login with in-flow selection. The glossary, the deviations
  register and the design coverage matrix are updated to match.

## Testing Decisions

A good test here fixes externally observable behaviour and nothing else: what a
token contains, what a login refuses, what a decision returns, what an isolation
boundary rejects. Tests that assert the shape of internal configuration or the
sequence of internal calls will be rejected in review — every decision above is
observable from outside the module that implements it.

- **Tenant registry** — unit tests over parsing, validation and resolution:
  duplicate realms, missing issuer, unreadable secret reference, unregistered
  realm, and idempotent re-seeding. No Keycloak, no network.
- **Tenant resolution** — unit tests mapping hosts to tenants, including unknown
  and malformed hosts.
- **Token verification** — table-driven tests in the existing style: a token from
  each registered realm verified, a token from an unregistered realm refused, a
  token whose issuer and audience disagree refused, tenant taken from the realm
  rather than any claim, hospital taken from the organization claim, and a token
  with neither an organization nor the tenant-wide marker refused.
- **Organization selection decision class** — JUnit over the pure decision: zero
  memberships as a non-administrator, zero as an administrator, one membership,
  several memberships, an alias pre-requested in scope that is and is not a
  membership, and the administrator tenant-wide entry.
- **Identity directory contract** — the existing shared contract suite is
  extended with the organization reads and with cross-tenant rejection, so every
  adapter must behave identically. Prior art: the existing directory contract and
  the dual-dialect store contract suite.
- **Decision service handlers** — a token from tenant A cannot read tenant B's
  data; a token without an organization is refused; the principal presented to
  the policy engine carries the realm and organization and ignores anything in
  the request that names a tenant or hospital.
- **Policies** — the exhaustive matrix is extended with the tenant-wide case per
  resource-action, and a focused suite covers the adversarial negatives, chiefly
  a tenant-wide session attempting to use a hospital-narrowed assignment and a
  hospital-scoped session attempting another hospital's.
- **Architecture tests** — added to the existing suite: no consumer derives
  tenant or hospital from request input; no decision path reads the memberships
  list; no single-realm identity configuration remains; no precedence ordering in
  Go.
- **Login flow end to end** — curl plus a cookie jar in the style of the existing
  identity suite: a single-membership user is not asked; a two-membership user is
  asked and each answer yields a different hospital in the token; a user cannot
  obtain a token for an organization they do not belong to; a membership-less
  user is refused; an administrator is offered and can take the tenant-wide
  entry; a deep link survives login; a silent switch yields a token for the other
  hospital.
- **Load and seeding** — the direct-grant organization scope spike; the seeding
  path verified for organizations and memberships; a preflight that fails fast
  rather than halfway through a five-realm bulk load.

## Out of Scope

- Writing organizations or memberships from this platform. Keycloak remains the
  only place they are created and changed.
- Home-realm discovery by email domain, identity brokering between realms, and
  any cross-realm single sign-on. A user authenticates in exactly one realm.
- Cross-tenant users. A person who works for two hospital groups has two
  accounts, one per realm.
- Multiple simultaneous hospital sessions in one browser profile. A session has
  one active hospital.
- Migrating existing assignment data to the new identifiers. Seed and demo data
  is regenerated.
- Organization hierarchies, such as departments within a hospital.
- Changing the permission precedence rules, or moving any part of them out of the
  policies.
- Adding a browser automation toolchain.

## Further Notes

- The single most consequential change is that tenant isolation moves from an
  attribute to a signature. Everything else follows from that.
- The organization-scope spike is the one item that can invalidate later work: if
  the organization claim is only populated in browser flows, the load model must
  change. It should be done first, alone.
- The design document's §7.1 mapping-mode contract is deliberately not
  implemented. That is a knowing deviation, not an omission, and belongs in the
  deviations register with its reasoning.
- The alias-as-identifier decision means an organization rename in Keycloak
  silently orphans assignment rows. Acceptable for a prototype; it would need a
  rename-detection reconciliation in a product.
- Seeding five realms multiplies the bulk-load cost. Expect this to dominate the
  wall-clock time of a full environment build, and to be the first thing that
  needs parallelising.
