# Cerbos POC adaptation requirements for PharmacyGUI and CSI-IAM

| Field | Value |
|---|---|
| Status | Discovery prerequisite and implementation gate |
| Target application | `phr-pharmacygui` (PHR) |
| Target identity platform | CSI-IAM on the required Keycloak **26.7.3** baseline |
| Authorization target | Cerbos PDP plus an Authorization Decision Service (ADS); Keycloak supplies verified identity context |
| Evidence boundary | Only `cerbos-poc/`, `phr-pharmacygui/`, and `csi-iam/` in this workspace |

## 1. Scope, terminology, and non-negotiable boundaries

This document identifies the work that must be complete before or as part of
adapting the Cerbos proof of concept (POC) to PharmacyGUI. It intentionally
makes no claim about backend services whose source is not present in the three
permitted repositories. Their ownership, endpoint inventory, and enforcement
implementation are therefore explicit delivery prerequisites.

The target boundary is fixed by the POC:

- **Keycloak / CSI-IAM authenticates** the user and issues a signed token carrying
  identity, active organization, and the active hospital's effective roles. It
  is not the Cerbos decision point.
- **ADS owns authorization data and decision context:** dynamic role grants,
  user overrides, authorization revisions, cache invalidation, and the trusted
  `permissionContext` sent to Cerbos.
- **Cerbos owns authorization policy and precedence.** The POC implements
  mandatory restriction > user revoke > user grant > role grant > default deny.
- **PHR business APIs are policy-enforcement points (PEPs).** Browser
  capabilities control rendering only; they do not authorize an API operation.
- **The browser receives evaluated capability snapshots only.** It must never
  receive Cerbos policy source, role grants, user overrides, or trusted
  resource attributes, and must never call the Cerbos PDP directly.

These are implementation facts, not optional design preferences. The POC
states the backend-PEP/browser-rendering boundary in
`cerbos-poc/docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md:21-24` and
server-side capability evaluation in
`cerbos-poc/apps/ads/internal/capability/capability.go:1-12`.

### 1.1 Target identity vocabulary

| Target fact | Authoritative source | Consumer rule |
|---|---|---|
| User | Verified token `sub` | Persist and compare the stable Keycloak user ID, not a username. |
| Tenant | Verified issuer / realm | Derive from the verified issuer; do not trust a tenant ID in a browser header or body. |
| Active hospital | Verified `organization` claim | Require exactly one organization alias for hospital-scoped operations. Where the legacy hospital ID is unique and immutable within the realm, use its canonical decimal string as the alias (for example, `"120045"`). |
| Legacy audit user (`x-user`) | Verified `csi_audit_context.employee_code` claim | Carry the existing `csi_employeeinfo.employee_code` as a decimal string. It is compatibility audit data mapped from the authenticated `sub`; it must not replace `sub` as the authorization principal. |
| Legacy audit group (`x-group`) | Verified `csi_audit_context.rms_tenant_id` claim | Carry the legacy RMS tenant/group ID as a decimal string. It is compatibility audit data mapped from the verified realm; it must not replace issuer/realm as the authorization tenant. |
| Legacy audit hospital (`x-hospital`) | Verified active `organization` claim | When the numeric-hospital-alias rule applies, inject the selected alias unchanged as the legacy hospital ID. The PEP derives authorization scope from the verified claim, not from the injected header. |
| Legacy audit location (`x-location`) | Authenticated server-side operational-location context | Optional operational context only. It must be validated against the verified active hospital and omitted when unset; it must never establish authorization scope. |
| Effective roles at the active hospital | Verified `organization_roles` claim derived from the active native organization's groups | Accept exactly `{"realm": string[], "client": {"<receiving-client-id>": string[]}}`, with either section omitted when empty and no other keys. Use only this claim; absent or malformed data grants no role-based permission, and there is no fallback to `realm_access` or `resource_access`. |
| Hospital membership list | `organization_memberships` claim | Alias-only display list. It must not establish decision scope or widen access. |
| Hospital display directory | Verified `csi_hospital_directory` claim | Display-only `{alias, name}` records for hospital selectors. Display names are not identifiers and must never be submitted or used for a decision. |
| Realm/client roles | Verified global token role claims | Preserve only for tenant-wide administration and legacy compatibility. Hospital-scoped PHR decisions never use them as a fallback. |
| Permissions and capabilities | ADS/Cerbos | Do not put role grants, user overrides, capability results, or `permissionContext` in an access token. |

The current POC token verifier validates signature, issuer, audience, validity
period, reserved roles, organization scope, and the strict `organization_roles`
shape before exposing an identity
(`cerbos-poc/libs/tokenverifier/tokenverifier.go:242-383`). In organization mode,
it canonicalizes only the active organization's structured role claim. Global
`realm_access` and `resource_access` claims remain visible for compatibility and
tenant-wide administration but can never grant a hospital-scoped permission.

### 1.2 Legacy transaction-audit compatibility boundary

The four legacy request headers are transaction-audit compatibility inputs, not
browser authority or Cerbos input. Preserve their existing numeric database
columns and the headers expected by unmodified backend services, but change
where the values are established:

```json
{
  "csi_audit_context": {
    "version": 1,
    "employee_code": "900123",
    "rms_tenant_id": "42"
  },
  "organization": ["120045"],
  "csi_hospital_directory": [
    {"alias": "120045", "name": "Riyadh Central Hospital"}
  ]
}
```

All identifier values in claims remain canonical strings even when the legacy
columns are numeric. This prevents JavaScript integer precision and formatting
changes; the trusted routing component sends the values unchanged in the
existing headers. The `csi_hospital_directory` claim contains display data
only, and must include only organizations that the authenticated user is a
member of.

For every browser-to-backend request, the PHR gateway/BFF or an equivalent
trusted edge component must:

1. verify the access token's signature, issuer, audience, validity period, and
   active-organization constraints;
2. discard all browser-supplied `x-user`, `x-group`, `x-hospital`, and
   `x-location` values;
3. derive `x-user` and `x-group` from the verified `csi_audit_context` claim;
4. derive `x-hospital` from the verified active organization alias when that
   alias is the canonical legacy hospital ID; otherwise resolve the documented
   alias-to-ID mapping server-side; and
5. derive optional `x-location` from a server-side authenticated operational
   context validated for the active hospital, then inject the resulting headers
   only on the private downstream request.

The normal request path must not query `csi_employeeinfo` or
`realm_tenant_mapping`: the employee code and RMS tenant ID are established at
token issue/refresh time. On a mapping change, revoke affected sessions or
force a fresh token; do not let stale claims survive beyond the access-token
lifetime. Location may change independently of the authorization hospital, so
updating it must update the server-side operational context rather than trusting
a browser header. A hospital switch requires a fresh organization-scoped token
and clears that location context.

Backend PEPs must continue to authorize from the verified token and
server-loaded resource attributes. They may retain the injected headers solely
to populate existing audit columns. Backend networks must accept such headers
only from the trusted gateway/BFF (for example, through private routing and
mTLS), so callers cannot bypass header stripping/injection.

## 2. Mandatory release gates

No PHR route should be cut over until all of these gates have named owners and
passing evidence.

1. **Version truth is reconciled.** Keycloak 26.7.3 is required because the
   approved role source depends on native organization groups and their role
   mappings. The checked-in CSI-IAM parent POM and current custom Admin UI
   packages are 26.0.1 (`csi-iam/csi-iam-extentions/pom.xml:5-20` and
   `csi-iam/csi-iam-ui/admin-ui/package.json:12-28`), while the POC provider and
   runtime are aligned to exactly 26.7.3
   (`cerbos-poc/apps/keycloak-org-selector/pom.xml` and `Dockerfile`). Establish
   the exact CSI-IAM image tag, source revision, Maven coordinates, Node package
   versions, JDK, and database migration path that form the coordinated 26.7.3
   release. `@csi/csi-auth-v2` version `26.2.11` in PHR is a library version,
   not proof that the checked-out server/provider source is 26.7.3.
2. **The PHR API inventory exists.** For every PHR read, mutation, print,
   export, workflow, and controlled-medication operation, name the endpoint,
   owning service, persistent resource identity, owner tenant/hospital, and PEP
   owner. The required backend source is not part of this evidence set.
3. **The identity/data mapping exists.** Map legacy RMS tenant IDs, hospital
   IDs, PHR pharmacy locations, organization aliases, users, roles, legacy
   permission records, and existing PHR client IDs. Identify source of truth,
   transformation, reconciliation query, and rollback action for every mapping.
4. **Compatibility contracts are characterized.** Capture representative
   requests and responses for PHR's current Keycloak-facing endpoints and token
   claims before changing their backing implementation. Capture each legacy
   transaction's expected `x-user`, `x-group`, `x-hospital`, and optional
   `x-location` values and the database columns they populate. Current PHR code
   consumes `logged-in`, current/default location, hospital IDs, and business
   permission responses.
5. **A reversible rollout exists.** Separate feature flags are required for
   realm/organization mapping, signed audit-context claims, trusted legacy-header
   injection, legacy endpoint adapters, PHR capability rendering, and backend
   PEP enforcement. Database backups/exports and a tested restoration procedure
   are required before any irreversible migration.

## 3. Keycloak 26.7.3 upgrade requirements

### 3.1 Align every server-side artifact to the selected patch release

A Keycloak provider JAR must be compiled and tested against the exact runtime
that loads it. The following work is required for the 26.7.3 image:

| Area | Current local evidence | Required action |
|---|---|---|
| CSI extension reactor | Parent and module coordinates are 26.0.1; Java target is 21. | Update the parent/version convention and every explicit module dependency/version to the approved 26.7.3 coordinates; compile the entire reactor with the release JDK. |
| CSI Admin UI and Account UI | The Admin UI uses 26.0.1 Keycloak JS/admin-client dependencies, while `@keycloak/keycloak-ui-shared` is 26.0.4. | Align its Keycloak dependencies and lockfiles to the server baseline, then rebuild its Maven theme assembly. |
| POC organization provider | Compiles against exactly 26.7.3 with Keycloak SPI, private SPI, services, and Jakarta REST APIs. | Port/recompile from source in the coordinated CSI-IAM 26.7.3 build, retain its unit tests, and execute real-server integration tests. Never copy a JAR built for another Keycloak patch into CSI-IAM. |
| CSI image assembly | The Dockerfile copies provider JARs and themes into `/opt/keycloak`, but does not run `kc.sh build`. | After every selected provider/theme is copied, run the appropriate `kc.sh build` during image creation, then start the optimized built image in integration tests. |
| Runtime configuration | The compose configuration passes Keycloak, database, proxy, Infinispan, and custom SPI settings. | Validate every setting against the selected 26.7.3 distribution; delete or replace only after contract tests prove no client/runtime dependency. |

The reactor currently lists 22 modules (`csi-iam/csi-iam-extentions/pom.xml:64-87`),
and the image copies their artifacts into the provider directory
(`csi-iam/Dockerfile:8-30`). A new module will not ship unless it is added to
that reactor and produces an artifact in the copied location.

### 3.2 Eliminate distribution and classpath assumptions

The POC provider uses `provided` Keycloak dependencies, avoiding duplicate
server classes in the provider JAR (`cerbos-poc/apps/keycloak-org-selector/pom.xml:30-63`).
Apply the same packaging discipline to CSI-IAM:

1. Audit all 22 modules for Keycloak/server-owned dependencies and ensure they
   are not shaded or bundled into provider JARs.
2. Replace legacy JAX-RS/server-module dependencies that are not compatible
   with the selected runtime. The parent still declares a JBoss `javax.ws.rs`
   specification and RESTEasy 3.15.6 (`csi-iam/csi-iam-extentions/pom.xml:49-60`);
   `login-module` additionally declares a JBoss servlet API and shades RESTEasy
   (`login-module/pom.xml:70-78,119-136`). Some resource classes still import
   `javax.ws.rs` (`user-login-details/.../UserLoginDetailResource.java:10-12`).
   Make each affected resource/provider consistently Jakarta-based and test it
   in the final image.
3. Build and boot the final image with all intended JARs. A clean build alone is
   insufficient: provider discovery, service registrations, JPA entity
   providers, authenticators, protocol mappers, event listeners, realm-resource
   providers, themes, and Liquibase changes must load together.
4. Test fresh and upgraded database schemas on every production database engine.
   The image contains Oracle drivers (`csi-iam/Dockerfile:22`) while local
   compose uses PostgreSQL (`csi-iam/docker-compose.yaml:43-46`), so neither
   engine can be inferred from the other.
5. Keep credentials and environment-specific service URLs outside committed
   deployment descriptors for the production release. The local compose file
   currently embeds a database credential and remote service URLs
   (`csi-iam/docker-compose.yaml:43-65`); use a secret-managed deployment
   configuration instead.

### 3.3 Prove Organizations on exactly 26.7.3

The POC's organization model depends on the `organization` feature and native
organization groups. Its 26.7.3 development deployment explicitly enables
`--features=organization` (`cerbos-poc/docker-compose.yml`). CSI-IAM must use the
same patch release for provider compilation, image build, schema migration, and
runtime verification; a mixed-version deployment is unsupported.

For every PHR realm:

1. Enable and validate the target release's Organizations capability and its
   realm import/onboarding behavior.
2. Define a stable, URL-safe organization alias for every authorization
   hospital. Prefer the canonical decimal legacy hospital ID as the alias
   (for example, `"120045"`) when it is unique and immutable within that realm.
   The `organization` claim and `organization:<alias>` scope treat this as a
   string, never a JSON number. This makes the verified active alias directly
   usable as legacy `x-hospital`. If that
   uniqueness/immutability condition does not hold, use a separate immutable
   alias and a documented server-side alias-to-legacy-hospital-ID adapter. Never
   use a hospital display name as an alias or reuse an alias after retirement.
3. Configure each native Keycloak Organization with a mutable display name as
   well as the immutable alias. Populate the name from the approved hospital
   directory during migration. A rename changes display data only; it must not
   alter organization membership, scope, roles, or legacy hospital identity.
4. Create membership for each authorized user and configure exactly one active
   organization per hospital-scoped token. A user with no organization scope
   must be denied for a hospital-scoped PHR operation; a token with multiple
   organization aliases is ambiguous, not broader access.
5. Create role-bearing groups under each native Keycloak Organization, map realm
   or receiving-client roles to those groups, and assign users to the groups.
   Configure the PHR client with the organization-role mapper so it emits only
   the selected organization's assignments as `organization_roles`. The exact
   shape is `{"realm": string[], "client": {"<receiving-client-id>":
   string[]}}`; either section is omitted when empty, arrays contain non-blank
   role names, and no foreign client or additional top-level key is accepted.
6. Configure the browser client for Authorization Code with PKCE, exact redirect
   URIs/web origins, explicit audience, the organization client scope, the
   organization-role mapper, and the audit/display claim mappers in §5.1. The POC
   switcher requests `openid organization:<alias>` and exchanges a PKCE
   authorization code through the browser application's top-level OIDC callback flow.
7. Define tenant-wide administrative roles through ordinary realm groups kept
   outside every Organization. The selector allows a no-organization route only
   for the configured admin realm role
   (`OrganizationSelectorAuthenticator.java`). ADS/PEPs must explicitly limit
   those sessions to tenant-wide operations; they receive no hospital scope or
   hospital-role fallback.
8. Reserve the POC platform role namespace from every client and group
   assignment. The POC verifier checks global and structured organization-role
   claims before scope resolution and refuses a token carrying a reserved role
   before any decision is made
   (`cerbos-poc/apps/ads/internal/tokenauth/tokenauth.go:75-101`).

### 3.4 Migrate context and permission ownership deliberately

CSI-IAM currently has overlapping stores that cannot be copied wholesale into
Keycloak or Cerbos:

| Existing concern | Local evidence | Adaptation requirement |
|---|---|---|
| Realm-to-RMS-tenant mapping | `REALM_TENANT_MAPPING` stores a unique realm/RMS tenant pair. | Retain the mapping as the source for the signed legacy audit `csi_audit_context.rms_tenant_id` claim. The existing `GroupClaimProtocolMapper` already reads `rms_tenant_id` at token issue time. ADS must derive authorization tenancy from the verified realm/issuer, not the legacy claim or a client-supplied value. |
| Employee identity to legacy audit user | `csi_employeeinfo` maps the Keycloak user ID to immutable `employee_code`; `logged-in` exposes that code as the legacy `id`. | Add a PHR client-scope mapper that resolves the authenticated `sub` to `csi_audit_context.employee_code` at token issue/refresh time. A missing or malformed value fails a transaction that requires `x-user`; do not query this mapping on every backend request. |
| Hospital membership and hospital roles | Legacy `HospitalRolesProtocolMapper` emits a `{hospitalId: [roleName, ...]}` map from custom hospital assignments. | Do not port this third role store into the authorization path. Migrate assignments to role-bearing native Organization groups and emit the selected organization's roles through the structured `organization_roles` claim. Reconcile migration counts before cutover; do not use membership display data or a browser-supplied hospital header as scope or authority. |
| Hospital identity and display name | `org_structure` separates `original_id` from `name`; hospital conversion preserves this as organization ID/code and display name. | On organization migration, preserve the canonical numeric hospital ID as the immutable alias where §3.3 permits, and copy the approved hospital name into the Keycloak Organization display name. Expose the name to the PHR browser only through the display-only directory claim; the login selector reads that native organization name directly. Aliases remain the only selector values. |
| Current hospital / location | `csi-user-detail/current-location` reads/writes a session-derived location; PHR stores `current-location` in local storage. | Replace the authorization-hospital part with a fresh organization-scoped token. Keep a pharmacy operational location as a separate server-side authenticated selection, validated as belonging to the active authorization hospital. It supplies optional legacy `x-location` after validation, never from browser storage/header. |
| Legacy transaction audit headers | PHR's installed security interceptor builds `x-group`, `x-hospital`, `x-location`, and `x-user` from `logged-in` data and browser storage before every request. | Preserve the backend header/column contract through trusted gateway/BFF injection. After verifying the access token, discard browser-supplied values; inject `x-user` from `employee_code`, `x-group` from `rms_tenant_id`, `x-hospital` from the active organization alias or approved mapping, and optional `x-location` from server-side operational context. The headers are audit compatibility data only, not PEP inputs. |
| Role, screen, feature, user, and business permissions | CSI-IAM exposes several custom permission APIs and its resource-permission provider emits a Kafka policy-sync event. | Extract and map semantics into the PHR resource/action catalog, ADS role assignments, user overrides, and Cerbos policies. Do not make these Keycloak tables/providers parallel authorization authorities after cutover. |

Relevant evidence is in
`csi-iam/csi-iam-extentions/tenant-realm-mapping/src/main/java/com/csi/tenantmapping/jpa/RealmTenantMapping.java:6-29`,
`token/.../GroupClaimProtocolMapper.java:61-82`,
`login-module/.../CSIUserRepresentation.java:47-62`,
`hospital-mapping/.../HospitalServiceImpl.java:209-315`, and
`token/.../HospitalRolesProtocolMapper.java:64-98`. The current PHR interceptor
also requires characterization before cutover: its fallback
`getLocationId` path assigns `currentLocation.hospitalId`, not
`currentLocation.locationId` (`phr-pharmacygui/node_modules/@csi/csi-security-appmenu/esm2015/lib/intercepter/intercepter.service.js:115-137`).

## 4. PharmacyGUI UI-library and client requirements

### 4.1 Choose a compatibility strategy before reusing POC web code

PHR is an Angular 7.2 application using TypeScript 3.2 and RxJS 6.3
(`phr-pharmacygui/package.json:19-29,322-323,336-355`). The POC web packages
are Angular 21.2, TypeScript 5.9, and RxJS 7.8
(`cerbos-poc/package.json:43-56`). Direct reuse is not possible: the POC uses
standalone directives, signals/effects, `inject`, functional guards, HTTP
context tokens, and functional interceptors
(`libs/web/capability/src/lib/capability-directive.ts:1-71`,
`capability-guard.ts:1-48`, and `capability-retry.interceptor.ts:1-48`).

Choose and approve one of these paths:

1. **Preferred: framework modernization.** Upgrade PHR and its dependency set
   to the POC-compatible Angular/TypeScript/RxJS baseline, then publish a
   versioned shared capability package from the POC implementation.
2. **Transitional: compatibility package.** Build and maintain an Angular 7
   compatible package that preserves the ADS capability API contract, using
   class-based guards/interceptors and RxJS 6. This is a separate product
   commitment; it must not copy unsupported Angular 21 constructs into PHR.

Either path requires a complete package compatibility matrix and a phased
upgrade plan for the large private CSI package surface before the first PHR
capability feature is released.

### 4.2 Required changes by UI library/integration boundary

| Library or surface | Current local use | Required change |
|---|---|---|
| `@csi/csi-auth-v2` | Initializes PHR auth, route guard, token access, logged user, and dynamic realm configuration. | Add a supported Keycloak 26.7.3 OIDC/PKCE integration, typed access to `iss`, `sub`, active `organization`, strict `organization_roles`, `csi_audit_context`, and display-only hospital directory claims; also provide token-refresh events and a fresh-token organization switch. PHR must not switch authorization context by writing browser storage. |
| `@csi/csi-security-appmenu` | Supplies menu, location-change, permission cache, and bearer `AuthInterceptor` behavior. | Replace its screen-permission cache inputs with a capability-aware menu adapter. Every menu/route/section/component needs a stable capability key. Remove its browser-side injection of `x-user`, `x-group`, `x-hospital`, and `x-location`; it may attach the bearer token only. On login, logout, token replacement, realm change, organization switch, or revision change, invalidate snapshots and reload the correct module snapshot. |
| `@csi/csi-base-library-version2` | PHR components call legacy visibility/editability helpers. | Add named capability bindings or make temporary compatibility helpers read only the evaluated capability snapshot. They must not reconstruct permissions from legacy screen data. |
| `@csi/csi-auth-popup-v2` | Used for authorization-related popup flows. | Separate reauthentication/MFA from authorization. Replace business-permission evaluation with a PEP-protected operation or an ADS capability pre-check. |
| `@csi/csi-services-gateway` and PHR HTTP services | Supplies user/hospital gateway state and many PHR endpoint base URLs. | Add a typed ADS/BFF capability endpoint, bearer propagation, revision handling, and a single stale-snapshot retry. Route legacy backend calls through a trusted gateway/BFF that strips browser `x-*` audit headers and injects verified claim/context values. Do not attach client-decided permission or hospital headers. |
| AG Grid and table wrappers | Numerous PHR screens use grid/rendered row actions. | Extend list-response contracts with batched row decision maps. Bind row actions to those results; do not issue one capability call per rendered row. |
| Local/session storage | Stores `current-location`, logged-user details, names, and operational location values. | Remove authorization decisions, active authorization hospital, and legacy audit header inputs from storage. Clear identity-dependent PHR state on token/context change. Retain only non-authoritative preferences; location changes must be submitted to and validated by the server-side operational-context route. |

PHR presently configures the first four libraries in its root module
(`phr-pharmacygui/src/app/app.module.ts:14-23,62-95,111-168`) and registers the
security-menu `AuthInterceptor` in the root and several feature modules. The
new retry interceptor must be ordered after bearer-token attachment and must be
registered once per HTTP injector hierarchy; otherwise a 403 may be retried
multiple times.

### 4.3 Replace the current PHR authorization/context flows

The PHR bootstrap guard currently:

- loads a broad `logged-in` object;
- takes `current-location` from local storage or `defaultLocation`;
- passes selected hospital/group values into gateway state; and
- calls `saveModulePermissions('phr', user.uid, hospitalId)`.

See `phr-pharmacygui/src/app/_guard/baseutil.guard.ts:186-245`. This flow must
be split into identity, authorization context, operational configuration, and
UI capability loading:

1. Complete authentication and obtain a verified, organization-scoped access
   token.
2. Decode claims only for display. Treat the token as the source of active
   authorization hospital; do not accept the old local-storage hospital as
   authority.
3. Resolve the organization alias to legacy hospital/group identifiers only for
   PHR domain services that still require them. Their PEP must independently
   validate that mapping.
4. Load non-security configuration and a module-level PHR capability snapshot.
   Configuration is not authorization and must not silently grant an operation.
5. Load instance snapshots only once the PHR resource is selected, passing route
   identifiers—not clinical attributes—to ADS.
6. On organization switch, obtain a fresh token, clear identity-dependent
   memory/storage/configuration, reload PHR configuration for the mapped
   hospital, and reload module capabilities.

PHR also directly calls
`/csi-user-detail/evaluate-bp` from services, for example
`item-master/.../generic-basic-info.service.ts:176-179`. These boolean checks
must be catalogued and replaced. The local source contains at least floor-stock
examples for print, prepare, submit, collect, reject, cancel, edit, and view;
the names demonstrate why a one-to-one migration of a single Boolean permission
is unsafe.

### 4.4 PHR capability contract

The POC client expects a snapshot containing authorization revision, policy
revision, catalog revision, tenant, hospital, module, context fingerprint, and
capability decisions (`cerbos-poc/libs/web/capability/src/lib/capability-decision.ts:7-32`).
The target PHR adapter/package must provide this equivalent contract:

```text
POST <PHR ADS/BFF base>/internal/capabilities/evaluate
Authorization: Bearer <access token>

{
  "module": "phr",
  "capabilityKeys": ["phr.route.inventory", "phr.action.dispense"],
  "context": {"prescriptionId": "..."}
}
```

The endpoint must derive principal, tenant, active hospital, roles, resource
ownership, and authorization attributes server-side. The POC rejects a request
without verified identity and accepts only `module`, `capabilityKeys`, and
routing context (`cerbos-poc/apps/ads/internal/capability/capability.go:119-149,182-200`).

A PHR client must:

- fetch a module/collection snapshot at session start and after every context
  switch;
- keep snapshots in memory, partitioned by identity/context/revisions;
- fetch an instance snapshot for a selected protected resource;
- use capability keys for route guards, menu entries, section visibility,
  editability, workflow buttons, print/export, and row actions;
- invalidate affected snapshots when a backend response reports changed
  authorization/catalog revisions; and
- on a 403 caused by a stale snapshot, invalidate and refresh once, then show
  the final denial. The POC retry guard is deliberately bounded to one retry
  (`libs/web/capability/src/lib/capability-retry.interceptor.ts:14-46`).

A route guard and a hidden button remain user-experience controls only. Every
PHR business endpoint must enforce the same resource action server-side.

## 5. Required Keycloak SPI and API work

### 5.1 Providers required for the organization-scoped PHR role model

| Requirement | Provider type | Required behavior and implementation condition |
|---|---|---|
| Active organization selection during interactive login | `Authenticator` and `AuthenticatorFactory` | After credentials and required MFA, read native organization membership. Automatically choose one membership; otherwise present only authorized choices. Render the organization display name but submit only its immutable alias. Re-read membership and administrator status on form submission so a tampered alias is rejected. The POC implementation is `OrganizationSelectorAuthenticator` and factory. |
| Legacy audit user and group | New PHR OIDC `ProtocolMapper`, optionally composed from the existing group mapper | Emit the versioned access-token claim `csi_audit_context: {version, employee_code, rms_tenant_id}`. Resolve `employee_code` from the authenticated user mapping and `rms_tenant_id` from the verified realm at issue/refresh time. Do not expose a mutable header/claim input and do not make a per-request database lookup. The values are audit compatibility data, not authorization input. |
| Hospital-scoped effective roles | POC `OrganizationRolesMapper` | Port and configure the mapper on the PHR client. It reads role mappings from only those native Organization groups that contain the user under the active organization and emits the strict `organization_roles` object. This claim is the sole ADS role source for a hospital-scoped token. |
| Organization membership display for the PHR switcher | OIDC `ProtocolMapper` | Retain `organization_memberships: string[]` containing only the aliases the user belongs to. It is display data only. The verified active `organization` claim remains the authorization scope. |
| Hospital display directory for selectors | New OIDC `ProtocolMapper` | Emit `csi_hospital_directory: [{alias, name}]`, restricted to the authenticated user's organization memberships. `alias` is the selector value and `name` is the visible label. Do not include roles, permissions, or decision data, and never use the name to establish scope. |
| Organization selection login page | CSI login theme addition | Merge/adapt `select-organization.ftl` into the CSI `csi` login theme, retaining existing CSI OTP/MFA pages, styles, localization, and accessibility. Each option's submitted value must be the alias and its displayed text must be the hospital name. |

The POC source establishes the selection and membership-mapper registrations
through `META-INF/services`, includes a small theme, and builds one provider JAR
(`cerbos-poc/apps/keycloak-org-selector/`). Its selector validates the submitted
alias against a freshly read membership list
(`OrganizationSelectorAuthenticator.java:61-93`); its mapper reads membership
through `OrganizationProvider` and emits aliases
(`OrganizationMembershipsMapper.java:63-71`). The current selection theme also
renders that alias as both the option value and visible text
(`select-organization.ftl:13-18`). The CSI adaptation must change only the
visible text to the trusted hospital name; it must retain the alias as the form
value and revalidation input. The PHR switcher must likewise render
`csi_hospital_directory[*].name` and request a fresh token using the paired
`alias`.

As historical migration evidence, CSI-IAM separately registers
`HospitalRolesProtocolMapper` as `oidc-hospital-roles-mapper` and invalidates its
`hospital_roles` session note when legacy assignments change
(`hospital-mapping/.../HospitalServiceImpl.java:73-80,171-205`). That mechanism
must remain outside the new authorization path and may be retired only after
native Organization-group assignment reconciliation succeeds.

Port the POC sources—not their built artifact—to the CSI-IAM 26.7.3 provider
reactor. Add their JAR to the final CSI-IAM image, run the image build step, and
configure the providers, mappers, client scope, login flow, and theme in every
PHR realm.

### 5.2 Changes required to existing CSI-IAM extensions

| Current extension / API | Required disposition for Cerbos adaptation |
|---|---|
| `token` / `HospitalRolesProtocolMapper` | Treat as a legacy migration source only. Do not port its custom assignment map into the new authorization path; reconcile its rows into native Organization groups, then retire or isolate it after dependent legacy consumers are migrated. |
| `token` / legacy audit and display mappers | Retain/port `GroupClaimProtocolMapper` without breaking existing consumers, and add the versioned PHR `csi_audit_context` mapper plus the display-only `csi_hospital_directory` mapper. The first emits stable employee/RMS tenant audit identifiers at token issue/refresh time; the second emits paired numeric-string alias/name options only for actual memberships. Neither mapper supplies authorization grants or PEP scope. |
| `hospital-mapping` and `org-structure` | Reconcile native organization aliases/memberships and migrate legacy role assignments into native Organization groups. Prefer the immutable canonical numeric hospital ID as the organization alias when valid per §3.3; otherwise retain a documented audit-header adapter. Any change in visible hospital lists needs a compatibility contract and per-realm rollout. |
| `tenant-realm-mapping` | Keep/adapt the mapping as the source for the `rms_tenant_id` audit claim and for legacy tenant-addressed callers. ADS uses the verified issuer/realm as its authorization tenant and must not query this SPI in its decision path. |
| `login-module` / `csi-user-detail` | Continue `logged-in`, profile, and default-location routes behind a compatibility contract until PHR no longer needs them. Remove `evaluate-bp` and `bulk-evaluation` from the authorization authority path; they accept an `x-hospital` header today, which must never define Cerbos scope. The current-location route may update only server-side operational location after validating it under the active token hospital. |
| `user-permission`, `screen-permission`, `feature-permission`, `business-permission`, `dynamic-permission`, and `resource-permission` | Retire them as authorities for PHR operations after equivalent ADS/Cerbos policies and PEPs exist. A temporary facade may translate old responses from the new authoritative decision path, but must have revision semantics, auditability, a feature flag, and an owner. |
| `event-kafka` and resource-permission Kafka publishing | Retain only for approved IAM/legacy integration events. ADS administration writes—not Keycloak permission/JPA callbacks—must own transactional permission-change outbox events and ADS cache invalidation. |
| MFA, SMS, email, session limiter, event store, user-login details, response handler, RMS integration | Port and test independently if existing product flows still require them. They are Keycloak release prerequisites, not Cerbos authorization SPIs. |

The current user-detail resource demonstrates the legacy risk: `evaluate-bp` and
bulk evaluation take the hospital from an `x-hospital` request header
(`csi-iam/csi-iam-extentions/login-module/src/main/java/com/csi/userdetail/rest/UserDetailResource.java:317-353`).
The user-permission resource also exposes the legacy `navigate` and `version`
permission APIs (`user-permission/.../UserPermissionResource.java:35-70`).
Those endpoints may be preserved temporarily for compatibility but must not
make a final Cerbos decision based on browser-provided scope.

### 5.3 APIs that must be added or changed

1. **Adopt the POC token verifier and ADS organization-role contract.** After
   signature, issuer, audience, and expiry validation, reject reserved roles
   across every role-bearing claim before organization scope resolution. For a
   hospital-scoped token, validate and canonicalize only the strict
   `organization_roles` object. A missing or malformed claim is rejected; an
   empty object grants no role permissions; and `realm_access` or
   `resource_access` never acts as a fallback. Tenant-wide sessions use only
   ordinary administrative group roles and carry no `organization_roles`.
2. **Add the legacy audit-context and operational-location contracts.** Configure
   the PHR client scope to issue `csi_audit_context` as an access-token claim
   containing exactly `{version, employee_code, rms_tenant_id}`. Configure the
   membership and directory claims in §5.1, preserving string alias values. Add
   an authenticated operational-location route that derives user and active
   hospital from the verified token, validates the requested location belongs to
   that hospital, and writes only server-side context. A location change must not
   change authorization scope; a hospital switch must obtain a fresh token and
   clear the location context.
3. **Add a trusted legacy audit-header injection filter at the PHR gateway/BFF.**
   It must verify the bearer token before routing, remove every incoming
   `x-user`, `x-group`, `x-hospital`, and `x-location` header, and inject only
   the values defined in §1.2. It must make no per-request query to
   `csi_employeeinfo` or `realm_tenant_mapping`, omit `x-location` when no
   validated context exists, and fail the request when a required audit mapping
   is absent or malformed. Restrict each legacy backend so only the gateway/BFF
   can send the injected headers. The filter must never pass the audit claims or
   injected headers to ADS/Cerbos as authorization facts.
4. **Keep the POC role-matrix scope aligned with the requirement.** Its current
   query is tenant + canonical role + resource, while hospital scope is used for
   user overrides (`cerbos-poc/apps/ads/internal/assignments/resolver.go:65-105`).
   This supports a user having different role names at different hospitals,
   because the verifier exposes only the active hospital's role list. If the same
   role name needs different permission grants by hospital, extend the role
   permission key, matrix query, cache key, administration API, and migration
   model with hospital scope before cutover.
5. **Add the PHR ADS/BFF capability endpoint** described in §4.4. It must be
   bearer-protected and use a server-side target resolver for every PHR
   capability. The POC's target resolver contract explicitly limits the browser
   to routing identifiers and requires server-loaded authorization attributes
   (`cerbos-poc/apps/ads/internal/capability/capability.go:49-75`).
6. **Add PEP integration to each PHR business API.** Before executing an action,
   verify the bearer token, load the authoritative resource, derive tenant,
   hospital, status, and other policy attributes, request the specific ADS
   action, and return 403 on denial. The backend must log correlation and
   decision IDs without logging tokens or clinical attributes. It may read the
   trusted injected audit headers only to populate unchanged legacy audit
   columns.
7. **Return authorization/catalog revisions in PHR API responses where an
   instance capability can become stale.** This lets the client invalidate the
   correct snapshot rather than indefinitely displaying a stale decision.
8. **Version additive replacements before removing existing CSI IAM routes or
   claims.** `logged-in`, `current-location`, `navigate`, `version`, and
   `evaluate-bp` need named consumer evidence before retirement. Roll out the
   26.7.3 server, provider, schema, mapper configuration, and PHR claim consumer
   as one coordinated release. Invalidate all existing user, offline, and SSO
   sessions at cutover so no token or session note minted under the legacy role
   source survives into the `organization_roles` authorization path.
9. **Secure and characterize all retained custom realm-resource endpoints.**
   Several current user-detail methods have authorization calls commented out,
   including user lookup routes (`UserDetailResource.java:145-220`). Do not copy
   that behavior into the new PHR/ADS integration; explicitly test every route's
   intended authentication and authorization requirement.

### 5.4 Explicitly out of scope for Keycloak

Do not add any of the following as part of this adaptation:

- a Keycloak policy provider that calls Cerbos per business request;
- a Keycloak schema for Cerbos role grants, user overrides, capability
  definitions, authorization revisions, or `permissionContext`;
- a token mapper that emits evaluated capabilities or Cerbos permission data;
- a browser API that submits trusted principal, role, tenant, hospital, or
  resource attributes to Cerbos; or
- a Keycloak-assigned replacement for the POC synthetic policy-evaluator role.

These additions would move authorization authority out of ADS/Cerbos and make
client or identity-platform state a second decision system.

## 6. PHR resource and policy prerequisites

A safe adaptation is not a UI-only implementation. Before writing PHR policies,
produce an approved resource/action catalog containing:

- each PHR resource's persistent ID, owner tenant, owner hospital, and
  authoritative backend;
- collection, instance, workflow, print/export, and controlled-medication
  actions;
- contextual facts that PEPs must derive, such as resource status, stock
  location, medication category, prescription/workflow state, patient linkage,
  approver, and regulatory constraints;
- the mapping from current PHR screen/business-permission keys to named PHR
  capability keys and one or more resource/action requirements; and
- migration cases where a legacy Boolean is not equivalent to an action.

At minimum, catalog the PHR modules exposed by the route tree: item master,
workbench, inventory, order, floor stock, configurations, narcotic-controlled
medication, protocols, and automated dispenser
(`phr-pharmacygui/src/app/app-routing.module.ts:18-161`). The POC's generic
FHIR medication/inventory vocabulary is a reference starting point, not an
automatic authorization model for PHR workflows.

## 7. Required acceptance evidence

### 7.1 Identity platform and provider evidence

- Full CSI-IAM image build and boot on the final 26.7.3 tag, with no provider
  discovery/load errors.
- A provider-load test for every retained custom provider and every new POC
  organization provider.
- Fresh-schema and upgrade-schema tests for each supported production database.
- Token contract tests for signature/issuer/audience/expiry, one active
  organization, canonical numeric-string aliases, unscoped and ambiguous
  tokens, role-bearing native Organization groups, exact `organization_roles`
  shape and types, expected effective hospital roles, no global-role fallback,
  malformed structured-claim rejection, alias-only membership display claim,
  membership-constrained `{alias, name}` hospital-directory claim, canonical
  role IDs, `csi_audit_context` shape/value/type validation, and reserved role
  rejection before scope resolution.
- Authentication-flow tests for no membership, one membership, multiple
  memberships, forged/stale form selection, tenant-wide administrator, MFA
  ordering, organization switching, and a displayed hospital name whose
  submitted value remains its alias.
- Theme visual/functional tests for the organization-selection screen and all
  CSI login/MFA/session-limit pages retained in the target image. Include an
  all-numeric alias rendered with its hospital name, not with an identifier-only
  label.

### 7.2 PHR and backend evidence

- A PHR test for every migrated capability key: route, menu, section, form
  field, workflow action, print/export, and grid row action.
- PHR tests that context switching gets a fresh token, clears stale client state,
  clears the operational location on hospital switch, reloads configuration, and
  reloads capabilities.
- Tests that no PHR component uses local storage, a custom hospital/audit header,
  or membership/display data as authorization authority; selector labels must
  use hospital names while submitting aliases only.
- Gateway/BFF tests that remove browser-supplied `x-user`, `x-group`,
  `x-hospital`, and `x-location`; inject claim-derived `x-user` and `x-group`,
  active-organization-derived `x-hospital`, and only validated optional
  `x-location`; reject missing required audit claims; and issue no database query
  per protected backend request.
- ADS tests that reject browser-supplied tenant, hospital, principal, roles,
  permissions, and resource attributes.
- PEP tests for allow, deny, tenant isolation, hospital isolation, mandatory
  constraints, stale snapshot 403 behavior, and unchanged writes to the legacy
  numeric audit columns from trusted injected headers only.
- Load/UX tests proving that collection and row decisions are batched and that a
  403 triggers at most one snapshot refresh.
- Characterization tests for every retained CSI-IAM endpoint and legacy claim,
  including the legacy `x-location` fallback discrepancy, plus a per-realm
  configuration-only rollback drill.

## 8. Recommended delivery sequence

1. Reconcile the version truth and build a clean 26.7.3 CSI-IAM image with its
   existing providers and theme under characterization tests.
2. Prove Organizations, native Organization groups and role mappings, the
   numeric-alias eligibility and name migration, strict `organization_roles`
   mapper, organization selector, ordinary tenant-admin groups,
   membership/directory mappers, audit-context mapper, and organization-switch
   behavior on that exact image.
3. Add the gateway/BFF audit-header injection filter in observe-only mode. For
   representative routes, compare its claim/context-derived values with current
   headers and downstream audit writes; correct mappings and the known location
   fallback discrepancy before it becomes authoritative.
4. Publish either the modern shared capability package or the explicitly owned
   Angular 7 compatibility package; first integrate it in a small PHR slice and
   remove its browser-side legacy audit-header injection.
5. Create the PHR resource/action catalog, PEP implementation, target resolvers,
   and Cerbos policies for that slice.
6. Enable trusted audit-header injection and remove only that slice's legacy UI
   checks and `evaluate-bp` calls behind per-realm flags. The browser sends its
   bearer token, not legacy audit identity/scope headers.
7. Repeat per vertical PHR workflow, retaining adapters until characterized
   consumers have migrated and reconciliation/rollback evidence passes.

This order separates Keycloak/provider compatibility, identity-context changes,
backend authorization enforcement, and PHR rendering migration so that a
failure has one diagnosable cause and a configuration-only rollback path.
