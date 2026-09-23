# Organization-scoped roles for the pilot

## Status

Approved design for the pilot release blocker: prove that one user can hold different effective roles in different Keycloak organizations and that ADS decisions use only the roles of the active organization.

## Problem

The current prototype proves that one user can select different organizations, but role assignment remains realm-wide. `user-doctor-multi` belongs to North Hospital and South Hospital while carrying the same `doctor` realm role in either session. ADS reads `realm_access.roles` or one client's `resource_access` entry without correlating those roles with the active organization.

Consequently, the prototype does not prove the pilot requirement that one person can be a doctor in one hospital and an auditor in another. Organization selection currently changes hospital scope but not effective roles.

## Goals

- Use Keycloak's native organization groups as the source of hospital-specific role assignments.
- Demonstrate one user as a doctor in North Hospital and an auditor in South Hospital.
- Emit an explicit, structured access-token claim containing only roles derived from the active organization's groups.
- Ensure ADS never falls back to global roles for a hospital-scoped session.
- Preserve tenant-wide administration through separate ordinary Keycloak admin groups.
- Prove the behavior through unit, integration, and real-stack decision tests.

## Non-goals

- Hospital-specific permission definitions for the same canonical role. The role-permission matrix remains tenant plus canonical role plus resource/action.
- Querying Keycloak during an ADS decision.
- Supporting native organization groups on Keycloak 26.2.5 or 26.4.
- Changing user-override precedence or Cerbos policy precedence.
- Introducing an ADS-owned organization-role assignment database.

## Keycloak baseline

Upgrade all runtime, build, and test baselines from Keycloak 26.4 to Keycloak 26.7.3. Native organization groups, organization-group membership management, organization-group role mappings, and the related provider APIs are required by this design.

The upgrade applies consistently to:

- the custom Keycloak image;
- the organization-selector provider's Maven dependencies;
- compose and TLS compose paths;
- Kubernetes image builds;
- the load-test Keycloak instance;
- fixtures, integration tests, and schema-sensitive load-seeding code.

A mixed 26.4/26.7.3 build or runtime is unsupported.

## Assignment model

Keycloak remains the source of truth for identity roles.

For the pilot fixture:

- North Hospital contains a Doctors organization group with the `doctor` realm-role mapping.
- South Hospital contains an Auditors organization group with the `auditor` realm-role mapping.
- `user-doctor-multi` is an organization member and a member of the corresponding group in both hospitals.
- Tenant-wide administrators belong to separately managed ordinary realm groups, not organization groups.

A user's organization membership determines where they may establish a hospital session. Membership in groups inside that organization determines which roles are effective in that session.

## Token contract

A custom OIDC protocol mapper emits `organization_roles` into the access token. It resolves the active organization from Keycloak's authenticated client-session context, obtains only that user's groups inside the active organization, expands composite role mappings, removes duplicates, and sorts output deterministically.

The claim shape is:

```json
{
  "organization_roles": {
    "realm": ["doctor"],
    "client": {
      "patient-app": ["care-team"]
    }
  }
}
```

Rules:

- `organization_roles` is present only when exactly one organization is active.
- `realm` is an optional array of realm-role names.
- `client` is an optional object keyed by client ID, with arrays of client-role names.
- The mapper emits client roles only for the OIDC client receiving the token.
- Empty arrays are omitted. An empty object is valid and means the active organization grants no roles.
- Unknown top-level fields, nulls, non-string entries, and incorrect container types are invalid.
- Duplicate roles are removed and arrays are sorted.
- Organization group paths are not authorization inputs and are not required in this claim.
- The mapper does not copy direct user role mappings or roles from ordinary realm groups into `organization_roles`.
- The mapper does not read an organization alias from request data; it uses Keycloak's trusted active-organization session context.

## ADS role-source behavior

Add an organization-scoped Keycloak role source to the token verifier and select it for the pilot deployment.

ADS verifies signature, issuer, audience, validity period, and active organization before interpreting `organization_roles`. It canonicalizes the structured roles using the realm established by the verified issuer:

- `organization_roles.realm: ["doctor"]` becomes `kc:<verified-realm>:realm:doctor`.
- `organization_roles.client.patient-app: ["care-team"]` becomes `kc:<verified-realm>:patient-app:care-team`.

For a hospital-scoped session:

- `organization_roles` is required and must have the strict shape above;
- only roles in that claim become effective roles;
- global `realm_access` and `resource_access` are ignored for authorization;
- client entries are accepted only for the configured token audience/client, and any other client key is rejected;
- no missing or malformed claim falls back to global role claims.

For an explicit tenant-wide session:

- the token must carry the existing tenant-wide administrator marker and no active organization;
- `organization_roles` must be absent;
- tenant-wide effective roles come from the existing global role claims assigned through separately managed ordinary tenant-admin groups;
- organization-group roles cannot become tenant-wide roles.

A token with an active organization and tenant-wide global roles still uses only `organization_roles`. This allows an administrator who is also a clinician to work under the same hospital-limited role set as any other clinician.

## Validation and failure behavior

The verifier rejects the token rather than silently dropping data when:

- `organization_roles` has the wrong JSON shape;
- `realm` or a client entry is not an array of strings;
- an unknown field is present;
- an unconfigured client ID is present;
- any raw role name begins with the reserved `sys:` prefix, case-insensitively;
- more than one organization is active;
- a hospital-scoped token omits `organization_roles`;
- a tenant-wide token contains `organization_roles`.

Reserved-role checks continue to cover global claims as defense in depth, even when those claims are not the selected hospital role source.

An empty, well-formed `organization_roles` object verifies successfully and produces zero effective roles. Subsequent authorization therefore denies unless an applicable user override grants the action.

## Decision data flow

1. The browser reaches the tenant realm and authenticates.
2. Keycloak selects or asks the user to select one organization.
3. The protocol mapper resolves the active organization from trusted session state.
4. The mapper finds the user's groups within that organization and emits their role mappings in `organization_roles`.
5. ADS verifies the token and derives tenant from the verified issuer and hospital from the verified organization claim.
6. ADS validates and canonicalizes only the active organization's structured roles.
7. Assignment resolution loads permissions for those canonical roles plus applicable user overrides.
8. Cerbos evaluates the existing permission context and tenant/hospital policy boundaries.

No decision-time request is made to Keycloak. A token remains the signed snapshot of identity and active-organization roles for its bounded lifetime.

## Role-matrix scope

The role matrix remains keyed by tenant and canonical role, with no hospital column. Different permissions across hospitals are represented by different role names becoming effective in each hospital. Thus `doctor` means the same thing everywhere in a tenant, while one user may be `doctor` in North and `auditor` in South.

If the pilot later requires the same canonical role to carry different permissions in different hospitals, that is a separate schema and administration-surface change.

## Test strategy

Implementation follows red-green TDD: each behavior is first expressed by a failing test, the failure is confirmed to be due to missing organization-role support, and only then is the minimal implementation added.

### Keycloak mapper tests

- North's Doctors group emits only `realm: ["doctor"]`.
- South's Auditors group emits only `realm: ["auditor"]`.
- Client mappings are grouped under the receiving client ID.
- Composite role mappings are expanded.
- Duplicate roles are removed and output is sorted.
- Groups from another organization do not contribute roles.
- Ordinary realm groups and direct user mappings do not contribute organization roles.
- No active organization emits no `organization_roles` claim.

A small pure claim-building unit is tested independently from Keycloak session plumbing. Real-server integration tests prove that the mapper receives the expected active organization and native group memberships.

### Token-verifier tests

- Realm and client roles canonicalize correctly using the verified realm.
- Hospital sessions ignore global role claims.
- Missing and malformed organization-role claims are rejected.
- Unknown keys, invalid values, and unconfigured client IDs are rejected.
- Reserved roles in organization or global claims reject the token.
- An empty claim produces no roles.
- Tenant-wide tokens reject organization roles.
- Hospital tokens cannot fall back to tenant-admin roles.
- Existing realm/client role-source tests remain valid for non-pilot configurations.

### Real-stack pilot acceptance

Use `user-doctor-multi` through the real organization-selection flow:

1. Select North Hospital and obtain a token.
2. Assert that North is active, the structured claim contains `doctor`, and it contains neither South's `auditor` role nor an unrelated global role.
3. Submit a matching North `patient_record:read` decision and assert it is allowed by the live doctor grant.
4. Authenticate the same user selecting South Hospital.
5. Assert that South is active, the structured claim contains `auditor`, and it contains no `doctor` role.
6. Submit an otherwise equivalent matching South `patient_record:read` decision and assert it is denied because the seeded auditor read grant is expired.
7. Include a global doctor role in the fixture/token path and prove it cannot make the South hospital session inherit North's permission.

The paired decisions keep tenant, principal, resource kind, action, status, and matching hospital relationship equivalent. The active organization's role is the intentional variable.

### Upgrade verification

- Compile and test the custom provider against 26.7.3.
- Import all realm fixtures on a clean 26.7.3 instance.
- Verify organization groups, memberships, role mappings, ordinary admin groups, and mapper configuration through the real server.
- Run relevant Go unit and integration tests.
- Run the organization selector, identity, hospital-switch, and new organization-role E2E suites.
- Run policy and architecture tests.
- Run the complete compose smoke suite.
- Validate the 26.7.3 schema assumptions in the direct-SQL load seeder and update them where required.
- Run the demo load-seed profile against a clean 26.7.3-backed load-test realm.

## Deployment and compatibility

The Keycloak upgrade and verifier role-source switch deploy together. Deploying the verifier first would reject old tokens because they lack `organization_roles`; deploying Keycloak first while ADS still reads global roles would not enforce organization-specific assignments.

Existing access tokens issued before activation are allowed to expire naturally only if the rollout keeps the old verifier active during a bounded transition. For the pilot environment, a coordinated restart that invalidates existing sessions is preferred because it makes the security boundary immediate and observable.

WSO2 and legacy Keycloak realm/client role-source modes remain separate configuration choices and are not silently changed to organization-scoped behavior.

## Acceptance criteria

The blocker is resolved only when all of the following are demonstrated on the running stack:

- Keycloak 26.7.3 is the consistent build and runtime baseline.
- One user has a doctor role in North Hospital and an auditor role in South Hospital through native organization groups.
- Each organization-scoped token contains only that active organization's structured roles.
- ADS canonicalizes and uses only those roles for hospital-scoped decisions.
- Global roles cannot leak into a hospital-scoped decision.
- Tenant-wide roles remain separate from organization-group roles.
- North and South produce the expected different authorization outcomes for the same user.
- Unit, integration, real-stack smoke, and demo load-seed verification pass.
