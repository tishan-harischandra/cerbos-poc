# ADR-012: Organization-scoped login, selected inside the login flow

A hospital is a Keycloak organization (ADR-010), and a user can belong to
several. Something has to decide, at login time, which one - if any - a
session is scoped to, and that decision has to be one Keycloak itself
attests to, not one the application layer negotiates afterward with a
claim it would then have to trust.

## The decision

**Organization selection is a Keycloak authenticator, inside the login
flow, after credentials and any second factor.**
`apps/keycloak-org-selector` is a custom authenticator SPI
(`OrganizationSelectorAuthenticator`) built into the platform's Keycloak
image. `OrganizationSelectionDecision` is a pure function - no Keycloak
API, no I/O - of the user's memberships, whether they hold the realm role
`admin`, and any organization alias already requested in the login's
`scope` parameter:

| Memberships | Administrator | Scope pre-requested a membership | Result |
|---|---|---|---|
| any | any | yes | selected silently, no screen |
| exactly one | no | no | auto-selected, no screen |
| more than one | no | no | selection screen, memberships only |
| any (including zero) | yes | no | selection screen, memberships plus a tenant-wide entry |
| zero | no | - | login refused with an explicit reason |

Being pure, `OrganizationSelectionDecisionTest` covers every row above
without starting Keycloak.

**An administrator's tenant-wide session is safe because "no hospital"
provably cannot mean "every hospital".** Choosing tenant-wide produces a
token with no active organization at all - not a wildcard, not every
membership at once. `libs/tokenverifier` treats an active hospital's
absence as its own state, and the resource policies (§5, `hospitalId`
matching) express the invariant directionally: a tenant-wide assignment is
reachable with no hospital, but a hospital-narrowed assignment never is
reachable without one. Nothing about the login flow or the token grants
scope; a tenant-wide session still reaches exactly the tenant-wide
assignments the role matrix actually contains, and nothing else.

**A non-administrator cannot forge tenant-wide.** The decision only offers
the tenant-wide entry when `admin` is present as a realm role Keycloak
itself checked, and rejects a submission naming it otherwise
(`org-selector-e2e.sh`, "a non-administrator cannot forge a tenant-wide
submission").

**Switching hospital during a session is a fresh, silent authorization
request**, not a client-side flag: the application asks Keycloak for a new
token scoped to the target organization against the existing SSO session,
discards the old token, and Keycloak's own authenticator re-runs the same
decision for the new scope (ADR-010's membership check applies again, so a
switch to an organization the user does not belong to fails the same way a
login would).

## Why in the login flow, not after

The alternative - let any client request any organization scope and trust
whichever comes back - would move the "does this user actually belong to
that organization" check out of Keycloak and into every consumer that
verifies a token, each of which would need to re-derive it from the
`organization` claim's absence rather than from an explicit refusal.
Keeping the decision inside Keycloak's own login flow means the guarantee
"this token's hospital is a membership Keycloak confirmed" is enforced once,
by the party that already holds the membership data, rather than asserted
by every downstream reader.

## Consequences

- **`redirect_uri` is never overridden by the authenticator.** Both the
  platform's Admin Console and Keycloak's own administration console are
  legitimate destinations; a deep link into either survives organization
  selection untouched (`console-crosslinks-e2e.sh`).
- **The memberships list in a token is display data, never a decision
  input.** `organization_memberships` (a custom protocol mapper,
  `OrganizationMembershipsMapper`) exists so a hospital switcher can offer
  every membership without a directory round trip; an architecture test
  forbids reading it anywhere on the decision path, only the single active
  `organization` claim.
- **Switching is a re-authentication, not a token refresh with a wider
  scope.** Measured directly (`docs/MEASURED_FINDINGS.md`): a refresh
  grant asking for a *different* organization than the one it already has
  is silently dropped, not honoured, so the hospital switcher performs a
  fresh authorization request instead. A *same*-scope refresh does
  preserve the active hospital correctly.
- A user with zero memberships and no `admin` role cannot log in at all,
  with an explicit reason rather than a generic authentication failure -
  the platform would rather refuse cleanly than issue a token that could
  never reach a hospital-scoped assignment anyway.
- The load harness (issue #87) exercises the same organization-scoped
  direct grant with no browser and no authenticator flow at all: measured
  directly that Keycloak's own organization scope handling for a direct
  grant does not require this authenticator - it is native to the
  `--features=organization` preview feature - so a 1,000-VU run is not
  measuring the authenticator's own cost.
