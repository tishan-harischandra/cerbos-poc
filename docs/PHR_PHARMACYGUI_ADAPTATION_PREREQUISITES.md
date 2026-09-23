# Prerequisites for adapting the Cerbos POC to `phr-pharmacygui`

| Field | Value |
|---|---|
| Status | Discovery and migration prerequisite document |
| Target identity platform | CSI-IAM rebased onto Keycloak **26.7.3** |
| Target UI | `phr-pharmacygui` (`PHR`) |
| Authorization model | Cerbos PDP + Authorization Decision Service (ADS); Keycloak authenticates and supplies identity context |
| Source evidence | Local Cerbos POC, PHR Pharmacy GUI, CSI-IAM, and CSI-IAM extension source checkouts |

## 1. Non-negotiable target model

The adaptation must preserve the Cerbos POC's trust boundary:

- **Keycloak authenticates users and establishes identity context.** It does not become the Cerbos policy decision point.
- **The ADS owns dynamic role grants, user overrides, authorization revisions, capability evaluation, and cache invalidation.** It is the only service that constructs `permissionContext`.
- **Cerbos owns policy logic and precedence:** mandatory deny > user revoke > user grant > role grant > default deny.
- **PHR backend APIs are policy enforcement points (PEPs).** A capability result controls rendering only; it never authorizes a backend operation.
- **PHR receives evaluated capability snapshots, not Cerbos policies, rule expressions, role grants, or trusted resource attributes.**

This is the POC's explicit architecture: browser-side capability rendering is not an enforcement boundary, and the ADS constructs the trusted context sent to Cerbos. See `docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md` §§4.2, 6.3–6.4, 11.3, and 12.

## 2. Version and source-of-truth gates

### 2.1 Observed version mismatch

Four independently versioned baselines exist in the local checkouts:

| Area | Observed state | Required decision / action |
|---|---|---|
| Cerbos POC Keycloak provider | POC provider and container image are pinned to Keycloak **26.7.3**. | Recompile and integration-test the provider in the coordinated CSI-IAM **26.7.3** build. Do not copy a provider JAR built for another patch into CSI-IAM. |
| CSI-IAM server checkout | Current `release/1.0.0` source declares Keycloak **6.0.1**, WildFly 16, Java 8-era dependencies. | Treat 26.7.3 as a full rebase/port, not an in-place dependency bump. |
| CSI domain extension source | `csi-iam-extentions-v2` declares Keycloak **6.0.1**, Java 8, `javax.*`, and WildFly module assumptions. | Port, replace, or retire each extension before deploying the 26.7.3 image. |
| PHR frontend | Angular **7.2**, TypeScript **3.2**, RxJS **6.3**. | Upgrade to a maintained Angular baseline before reusing the POC web capability library, which currently uses Angular 21, TypeScript 5.9, and RxJS 7. |

Evidence:

- POC provider pin: `apps/keycloak-org-selector/pom.xml` and `apps/keycloak-org-selector/Dockerfile`.
- POC runtime image and organization feature: `docker-compose.yml`.
- CSI-IAM 6.0.1 baseline: `../../../Security-new/CSI-IAM/Newiam/csi-iam-service/pom.xml` and `Dockerfile`.
- CSI extension baseline: `../../../Security-new/csi-iam-extentions-v2/pom.xml`.
- PHR frontend dependencies: `../../../Pharmacy/phr-pharmacygui/package.json`.

### 2.2 Gate before implementation

Before changing PHR or the production identity platform, create and approve a short compatibility register containing:

1. the exact CSI-IAM 26.7.3 source branch/tag and its build image;
2. source repositories and owners for every currently deployed CSI extension JAR;
3. current production realm, client, role, hospital, user-permission, business-permission, and screen-permission data exports;
4. the source of truth for each hospital identifier and PHR pharmacy-location identifier;
5. the target PHR API inventory, its resource owner, and its PEP owner;
6. browser client IDs, valid redirect URIs, web origins, audience configuration, and service-account roles for every environment; and
7. a rollback plan which preserves the pre-cutover identity database and existing PHR permission behavior.

**Do not point Keycloak 26.7.3 at a Keycloak 6.0.1 production database or deploy opaque legacy JARs into the new runtime.** First choose and prove a supported staged migration or a clean-target import/reconstruction path in an isolated environment.

## 3. Keycloak 26.7.3 upgrade requirements

### 3.1 Replace the server distribution, not just Maven versions

CSI-IAM's current image unpacks a WildFly-based `keycloak-6.0.1` archive, runs `jboss-cli`, adds JBoss modules, and starts `standalone.sh`. Keycloak 26.7.3 must use the supported Keycloak 26 distribution and its build/start lifecycle instead.

Required work:

1. Base the image on the approved Keycloak **26.7.3** distribution.
2. Replace WildFly subsystem configuration, `standalone*.xml`, JBoss CLI scripts, and `module add` commands with Keycloak 26 configuration and provider packaging.
3. Build providers into `/opt/keycloak/providers` and run `kc.sh build` once in the image build after all providers are present.
4. Translate legacy bootstrap, database, hostname/proxy, TLS, health, metrics, cache, and cluster settings to the Keycloak 26 configuration contract. Do not carry forward `KEYCLOAK_USER`, `KEYCLOAK_PASSWORD`, `DB_VENDOR`, or `standalone.sh` behavior without explicit compatibility verification.
5. Compile every provider against the 26.7.3 BOM/API supplied by the production image, using the JDK supported by that distribution. Keycloak APIs are not a cross-major binary compatibility promise.
6. Convert provider code and dependencies from `javax.*` to `jakarta.*`; review all JPA/Hibernate, RESTEasy, Infinispan, Jackson, Feign, logging, and serialization behavior under the new platform.
7. Remove bundled or server-module copies of Keycloak classes and server-owned libraries. Use explicit dependency scopes and verify `kc.sh build` reports no split-package or provider conflicts.

The POC already demonstrates the required provider delivery shape: Maven build, copy JAR into `/opt/keycloak/providers`, then `kc.sh build --features=organization` in `apps/keycloak-org-selector/Dockerfile`.

### 3.2 Data and realm migration requirements

The migration must maintain separate ownership boundaries.

| Data | Target owner | Requirement |
|---|---|---|
| Keycloak users, credentials, clients, realm/client roles, groups, identity-provider links, sessions, and standard realm configuration | Keycloak | Migrate through a verified 6.x-to-26.7.3 process or rebuild/import into a clean target. Validate all login and token flows before cutover. |
| Legacy CSI hospital and organization mappings | Keycloak Organizations or a dedicated domain service | Decide one authoritative representation. Map each hospital to a stable Keycloak Organization alias if it is an authorization hospital. Keep PHR's dispensing/pharmacy location separate if it is a sub-hospital operational location. |
| Legacy screen, business, feature, and user permission tables | ADS authorization database | Extract and map semantics to the POC role matrix and user override model only after business approval. They are not Keycloak authorization state in the target design. |
| POC role permissions, user overrides, revisions, audit log, outbox, resource metadata, and capability catalog | ADS authorization database and policy release | Deploy and migrate separately from Keycloak. No POC authorization table belongs in the Keycloak schema. |
| Existing generic CSI persistence events | Their owning integration | Preserve only after an event-contract review. They must not become the source of Cerbos permission invalidations. |

A data-mapping specification is required for every legacy record type. It must name the source table/API, target resource-action or identity field, transformations, unmappable values, reconciliation query, and rollback behavior. In particular, do not assume a legacy Boolean business permission maps one-to-one to a Cerbos resource action: PHR uses workflows such as dispense, prepare, deliver, print, and approve that require explicit business semantics.

### 3.3 Organizations, tenant, hospital, and active context

The POC semantic model is:

- **tenant** = the Keycloak realm that issued the verified token;
- **hospital** = the active Keycloak Organization alias in that realm;
- a user may be a member of multiple hospitals but a decision uses exactly one active hospital, or an explicit tenant-wide administrator context;
- hospital roles come only from role-bearing native Organization groups and the strict `organization_roles` claim; global roles are not a fallback.

Required Keycloak 26.7.3 configuration and tests:

1. Use exactly Keycloak 26.7.3 and enable `--features=organization` at build and runtime. Native Organization groups and their role-mapping APIs are required, so a mixed-version deployment is unsupported.
2. Enable Organizations for every PHR realm where hospital-scoped authorization is required.
3. Migrate or create organizations with immutable/stable aliases, membership, and administration rules. Define the alias-to-legacy-hospital-ID mapping once and expose it through an adapter where PHR still needs legacy IDs.
4. Configure an `organization` client scope and test an authorization request scoped as `organization:<alias>`.
5. Install the organization-selector authentication step after credentials and MFA. It must re-derive membership from Keycloak during form submission; it must not trust a submitted hospital alias.
6. Create role-bearing groups under each Organization, assign users there, and install the `organization_roles` mapper. Its exact object contains optional `realm: string[]` and `client: {"<receiving-client-id>": string[]}` sections and no other keys.
7. Install the `organization_memberships` OIDC protocol mapper for display-only hospital switching. The active `organization` claim is authoritative for the current decision; membership claims must never widen access.
8. Assign tenant-wide administrators through an ordinary realm group outside all Organizations. Tokens carrying any reserved `sys:` role must be rejected before scope resolution; the synthetic Cerbos evaluator role is never assigned by Keycloak.
9. Create a public PHR browser client using Authorization Code + PKCE, exact redirect URIs and origins per environment, and an audience accepted by each PHR PEP/ADS endpoint.
10. Create a separate confidential ADS/admin-directory service account with only the Keycloak Admin REST permissions needed to read users, roles, and organizations. No browser receives that credential.

### 3.4 Identity and token contract

The following is a cross-system contract, not an optional mapper convention:

| Fact | Authoritative source | Consumer rule |
|---|---|---|
| User | `sub` | Persist/use stable Keycloak user ID, never username. |
| Tenant | verified issuer plus trusted tenant registry | Derive the realm from the verified issuer; do not trust a `tenantId` in a browser body or header. |
| Active hospital | Keycloak active `organization` claim | Require exactly one alias, or an explicitly authorized tenant-wide context. |
| Other hospitals | `organization_memberships` custom claim | Display-only. Never use it for decision scope. |
| Hospital-scoped roles | `organization_roles` derived from native Organization groups | Validate the strict shape and normalize only its realm and receiving-client arrays. Never fall back to `realm_access` or `resource_access`. |
| Permissions and capabilities | ADS/Cerbos only | Do not encode role grants, user overrides, evaluated capabilities, or `permissionContext` in the access token. |

Every PHR PEP and the ADS must verify signature, issuer, audience, expiration, accepted algorithm, and role source. The POC verifier implements this contract in `libs/tokenverifier/tokenverifier.go`; its tenant and hospital data flow must be kept intact when PHR services are introduced.

## 4. CSI-IAM extension port, replacement, and retirement

### 4.1 Full inventory is mandatory

The existing image dynamically injects JARs as WildFly modules. The installed list includes organization structure, login/multimodule, screen permission, user permission, token, email, RMS integration, hospital mapping, business permission, dynamic permission, events, feature permission, and response-handler extensions. Source for a broad set of these exists under `../../../Security-new/csi-iam-extentions-v2` but currently targets Keycloak 6.0.1.

For each JAR, the owner must declare **port**, **replace**, or **retire** before the Keycloak 26.7.3 build starts. A binary-only JAR is a blocker: obtain its source and an automated test suite, or remove its runtime responsibility.

### 4.2 Required disposition for Cerbos adaptation

| Existing CSI customization | Evidence | Target disposition |
|---|---|---|
| CSI-IAM core fork: `CurrentLocationSpi`, current-location cache/provider, and modified session/Infinispan classes | `server-spi*/.../CurrentLocation*`, `model/infinispan/.../InfinispanCurrentLocationProvider*` | **Retire as a core patch.** Do not modify Keycloak's server SPI, session cache, or Infinispan implementation. Model active authorization hospital with Keycloak Organizations and token reissue. Retain a separate domain store only for PHR's operational pharmacy location if needed. |
| CSI-IAM core JPA interception and RabbitMQ persistence publisher | `DefaultJpaConnectionProviderFactory` and `CSIJPA*EventListener` classes | **Retire as a Keycloak core modification.** Keep generic audit/integration publishing in a separately ported provider only if still needed. Permission invalidation must originate from the ADS Administration Service transactional outbox, not ORM callbacks in Keycloak. |
| Organization structure and hospital mapping JPA/REST/SPIs | `csi-iam-extentions-v2/org-structure`, `hospital-mapping` | **Replace or narrow.** Use native Keycloak Organizations for authorization hospitals. Port only legacy hierarchy/profile APIs that PHR still needs, with a documented adapter between legacy hospital IDs and organization aliases. |
| `csi-user-detail` login/multimodule Realm Resource Provider | `login-module/.../UserDetailResource.java` | **Port selectively.** Preserve `logged-in` and required profile/default-location contracts during transition, but move authorization decisions out. Its current `evaluate-bp`, bulk business-permission, and header-driven hospital decisions must not remain authority. |
| Business, screen, user, feature, and dynamic-permission providers | `business-permission`, `screen-permission`, `user-permission`, `feature-permission`, `dynamic-permission-management` | **Retire as authorization authorities.** Migrate their data/UI semantics into POC resource actions, role permissions, overrides, and capability definitions. Provide a short-lived compatibility façade only where a consumer cannot migrate atomically. |
| Kafka event listener | `event-kafka/.../KafkaEventListenerProvider*` | **Port only for remaining IAM event consumers.** It can publish identity/audit events after a 26.7.3 compatibility and privacy review, but it is not the Cerbos decision path or assignment outbox. |
| RMS/Feign integration | `rms-integration` and CSI-IAM `RealmManager`/`RealmsAdminResource` | **Port or externalize.** Remove hard-coded service URLs; use explicit configuration, timeouts, auth, observability, and failure behavior. Keep RMS synchronization outside the authorization hot path. |
| MFA, SMS, email, token, user-session limiter, response-handler extensions | extension root modules | **Port independently if required by existing login/product flows.** They are Keycloak upgrade prerequisites but not Cerbos authorization SPIs. |
| Legacy login, admin, and FreeMarker themes | CSI-IAM `themes` and deployed custom themes | **Port and visual-regression test.** Add the POC organization-selection template to the target login theme, preserving accessibility and localization. |

### 4.3 Keycloak SPI/API requirements

Only the following new Keycloak extensions are required for the Cerbos POC identity experience:

| Capability | Keycloak extension point | Required behavior |
|---|---|---|
| Active-hospital selection during login | `Authenticator` + `AuthenticatorFactory` | After primary credentials/MFA, read native organization membership, select one membership automatically when unambiguous, otherwise show a controlled selection form, and refuse an invalid or unauthorized selection. |
| Membership list for the PHR hospital switcher | OIDC `ProtocolMapper` | Add `organization_memberships: string[]` from native organization membership. It is display data only. |
| Existing PHR profile/default-location APIs, only while consumers remain | `RealmResourceProvider` + factory | Port to Jakarta/Keycloak 26.7.3 and require verified bearer authentication for protected routes. Keep the response contract under versioned tests. |
| Existing custom domain entities that cannot be removed | JPA Entity Provider plus a versioned changelog | Isolate in provider JARs, scope by realm, supply a migration/reconciliation plan, and test provider discovery plus clean and upgraded databases. |
| IAM audit/integration events that still have consumers | `EventListenerProvider` + factory | Publish only approved, scrubbed identity/audit events. Do not use it for ADS role/user permission invalidation. |

The POC's organization selector and mapper are implemented in `apps/keycloak-org-selector`. It currently references `keycloak-server-spi-private` and `keycloak-services`; therefore it must be source-compiled and end-to-end tested against **26.7.3**, not treated as a portable binary.

### 4.4 Explicitly not required

Do **not** implement any of the following in Keycloak for this adaptation:

- a Keycloak `PolicyProvider` that calls Cerbos for every authorization decision;
- a Keycloak database schema for Cerbos resources, role permissions, user overrides, capability expressions, or revision counters;
- a token mapper that emits capability results or `permissionContext`;
- a browser API that submits `principal`, roles, tenant, hospital, permission grants, or resource attributes to Cerbos; or
- a replacement synthetic `sys:permission-evaluator` Keycloak role.

These choices would place policy evaluation or decision context in the wrong trust boundary and break the POC's data-only assignment and cache-convergence model.

## 5. PHR UI-library requirements

### 5.1 Current PHR integration points

PHR currently uses:

- `@csi/csi-auth-v2` and its Keycloak wrapper for initialization and route authentication;
- `@csi/csi-security-appmenu` for module/menu/screen permission loading and hospital-change events;
- `@csi/csi-base-library-version2` helpers such as component visibility and editability checks;
- `@csi/csi-services-gateway` and existing HTTP services;
- `PermissionModule.json`, a hierarchy of screens, sections, components, `isVisible`, `isEditable`, and removable rules; and
- `csi-user-detail` endpoints such as `/evaluate-bp`, while `CsiSecurityAppmenuService` calls `/navigate` and `/version`.

The current root guard also takes a hospital from local storage, reloads permission JSON for it, and passes it through legacy service calls. This is incompatible with the POC model, where the active hospital must be established by the verified access token.

### 5.2 Foundation upgrade

The POC Angular capability library is not source- or binary-compatible with PHR's current Angular 7 application. It uses standalone components/directives, signals, functional guards, `inject`, `HttpContextToken`, TypeScript 5.9, and RxJS 7; PHR uses Angular 7.2, TypeScript 3.2, class guards, and RxJS 6.3.

Required prerequisites:

1. Upgrade PHR to a maintained Angular/TypeScript/RxJS baseline compatible with the approved shared capability library. Do not backport copied POC source to Angular 7 or downgrade the capability library.
2. Upgrade and certify every CSI UI dependency against that same baseline, particularly `@csi/csi-auth-v2`, `@csi/csi-keycloak-angular`, `@csi/csi-security-appmenu`, `@csi/csi-base-library-version2`, `@csi/csi-services-gateway`, Nova, AG Grid, dynamic-form libraries, and PHR's remaining private packages.
3. Publish the capability library through the UI monorepo's public API/package workflow. PHR must import a versioned package; it must not import source files directly from the Cerbos POC or another library's internal path.
4. Complete a dependency compatibility matrix before the PHR framework upgrade begins. PHR currently has a large Angular 7 package surface, so the capability work cannot be separated from that dependency migration.

### 5.3 Required library changes

| Library / concern | Required change |
|---|---|
| New shared capability library | Publish a maintained capability package based on the POC contract: typed snapshot models, a snapshot client, an in-memory store, route guard helpers, structural/attribute directives, context keys, cache invalidation, and a one-time 403 stale-snapshot retry. It may not evaluate policy expressions in the browser. |
| `@csi/csi-auth-v2` and its Keycloak adapter | Support Keycloak 26.7.3 Authorization Code + PKCE, token refresh, typed token-claim access, token-change events, and an organization/hospital switch operation that obtains a fresh organization-scoped token. Preserve the dynamic realm resolver only if the derived issuer is in the trusted deployment configuration. |
| `@csi/csi-security-appmenu` | Replace the `navigate`/`version` screen-permission cache with a capability-aware menu adapter. Menu, route, screen, section, and component entries need stable capability keys. Invalidate all snapshots on login/logout, token replacement, realm change, hospital change, and authorization/catalog revision mismatch. |
| `@csi/csi-base-library-version2` | Replace `getComponentVisibility`/`getComponentEditability` as authorization inputs with named capability bindings. If compatibility methods remain temporarily, they must read a capability snapshot and not reconstruct permissions from legacy screen JSON. |
| `@csi/csi-auth-popup-v2` | Replace any business-permission popup evaluation with an explicit, server-authorized operation or a capability pre-check. Keep reauthentication/MFA behavior separate from authorization. |
| `@csi/csi-services-gateway` and HTTP layer | Add a typed ADS/BFF base URL and bearer-token propagation. Register the capability stale-snapshot retry after the bearer interceptor. It must refresh the snapshot once after a 403, then surface the final denial; it must never attach client-decided authorization headers. |
| AG Grid and table wrappers | Add batch row-level capability decisions to list responses. Do not make one ADS/browser request per row. Bind row actions to the response's evaluated decision map. |
| Browser storage | Retire PouchDB/localStorage permission decision caches as authorization state. At most, cache non-authoritative UI snapshots keyed by subject, issuer/realm, active organization, module, context fingerprint, authorization revision, and catalog revision; discard them on any identity or revision transition. |

### 5.4 PHR application changes enabled by those libraries

1. Replace `PermissionModule.json`'s implicit visibility/editability behavior with a mapping registry:
   `legacy screen/section/component/business-permission key -> PHR capability key -> resource-action expression`.
2. Model PHR routes as module- or collection-context capabilities. Examples include inventory list, item master list, workbench list, and pharmacy configuration routes.
3. Fetch an instance snapshot when an order, prescription, dispensation, stock adjustment, controlled medication record, or other protected resource is loaded. Pass only route identifiers to the ADS/BFF.
4. Map button, menu, form-field, print, and workflow controls to purpose-specific capabilities. Visibility and editability are separate decisions when their backend actions differ.
5. Replace every `/csi-user-detail/evaluate-bp` call, and the legacy `/navigate`/`/version` permission-loading path, with the approved capability snapshot or an actual PEP-protected operation.
6. Replace local-storage hospital switching with: select a verified membership, obtain a fresh organization-scoped token, clear PHR identity/config/cache state, load the new module snapshot, and reload hospital-scoped data.
7. Ensure disabled/hidden UI does not fetch or retain protected data. Backend APIs must independently deny the underlying resource-action.

## 6. PHR resource, policy, and backend prerequisites

This adaptation cannot be safely completed as a UI-only change.

### 6.1 Resource/action inventory

Before writing policies, produce a PHR authorization catalog that names:

- each PHR domain resource, its persistent identity, owning tenant, owning hospital, and authoritative data service;
- every collection, instance, workflow, print/export, and controlled-medication operation;
- the required contextual attributes: status, order type, prescription state, stock location, controlled-drug flag, care setting, patient linkage, department, approver, and any regulatory constraints;
- whether the POC's generated FHIR actions (`create`, `list`, `read`, `update`, `delete`, `assign`) are sufficient; and
- explicit domain actions where they are not sufficient, such as `prepare`, `validate`, `approve`, `dispense`, `deliver`, `cancel`, `hold`, `unhold`, `return`, `print`, and `export`.

The POC already contains generic FHIR medication and inventory resources, for example `medication_dispense` and `inventory_item`. They are a starting vocabulary only; they do not authorize PHR workflows automatically.

### 6.2 PEP and target-resolution work

For every PHR backend endpoint:

1. add a PEP integration that verifies the caller token and calls ADS before executing the business operation;
2. load the resource from the authoritative PHR service/database before authorization;
3. derive tenant, hospital, status, and other policy attributes on the server;
4. request the one operation-specific action from ADS/Cerbos;
5. return an explicit 403 for denial, with an authorization and catalog revision where an instance snapshot may now be stale; and
6. log a correlation ID, ADS/Cerbos call ID, decision source, and permission revision without logging sensitive clinical data or tokens.

The capability evaluator requires a PHR target resolver for every capability `targetRef`. It must translate a route identifier into a trusted PHR resource/collection; it must not accept resource attributes, tenant, hospital, roles, grants, or overrides from the browser.

### 6.3 Policy and capability ownership

| Asset | Owner | Change cadence |
|---|---|---|
| PHR resource catalog, schemas, root Cerbos policies, and composite capability definitions | PHR application/security engineering through the policy release process | Versioned, tested policy release |
| Role-to-resource-action matrix and user overrides | Authorization administrators through ADS Administration Service | Data change; no Cerbos policy rebuild |
| PHR resource ownership/status attributes | PHR domain services | Business transaction/change |
| Browser capability snapshot | ADS | Cacheable rendering hint only |

Capability definitions are application-owned, versioned expressions. PHR administrators must not assign capability keys directly; they assign resource-action permissions through the role matrix or approved user overrides.

## 7. Required API changes and compatibility boundaries

### 7.1 New/updated target APIs

| API | Caller | Required contract |
|---|---|---|
| Keycloak OIDC discovery, authorization, token, logout, and JWKS | PHR auth library and PEPs | Standard Keycloak 26.7.3 endpoints, Authorization Code + PKCE for the browser, verified issuer/audience/signature at PEPs. |
| Keycloak Admin REST users, roles, role mappings, organizations, and memberships | ADS Identity Directory adapter only | Dedicated read-only service account. Stable user and role IDs; canonical role normalization matches verified runtime tokens. |
| Capability snapshot endpoint | PHR browser through the approved gateway/BFF route | `module`, capability keys, and route identifiers only. Returns capability map plus authorization revision, root policy revision, catalog revision, active tenant/hospital, and context fingerprint. |
| ADS decision endpoint | PHR backend PEPs only | Trusted identity plus server-loaded resource context. Browser never calls Cerbos directly. |
| PHR business APIs | PHR browser/backend callers | Enforce their own resource-action. Provide 403 and revision metadata to support a single snapshot refresh. |
| ADS Administration Service | Authorization administration UI | Owns role-matrix and user-override writes, optimistic revision checks, audit, outbox, and capability-impact preview. |

### 7.2 Legacy API disposition

| Legacy endpoint/function | Current consumer behavior | Target handling |
|---|---|---|
| `/auth/realms/{realm}/csi-user-detail/evaluate-bp` | PHR sends `bpCodes` and a client-supplied `x-hospital`; response is Boolean. | Remove from PHR authorization flow. During a tightly controlled transition, a compatibility façade may map a legacy key to an ADS capability, but it must derive user/tenant/hospital from the verified token and return no policy context. |
| `/navigate` and `/version` | Security App Menu caches a hierarchical permission JSON. | Replace with capability snapshot/revision flow. Never retain the old payload as the authorization source. |
| `csi-user-detail/logged-in` | PHR consumes profile, modules, organizations, and default location. | Keep only the non-authorization profile contract until consumers migrate. Its organization output must reconcile with Keycloak Organizations. |
| `current-location` | Stores/returns current hospital/location in CSI-IAM. | Separate active authorization hospital (Keycloak token claim) from optional PHR operational location. Do not let this API change authorization scope by itself. |

## 8. Delivery sequence and exit criteria

### Phase 0 — architecture decisions

- Approve tenant/realm, hospital/organization alias, tenant-wide administrator, role-source, and PHR pharmacy-location models.
- Approve whether PHR needs PostgreSQL, Oracle, or both for the ADS data store; validate the POC schema and migrations against required database platforms.
- Complete the PHR resource/action and legacy-permission mapping inventory.
- Freeze a compatibility contract for PHR profile APIs and for the capability snapshot.

### Phase 1 — Keycloak 26.7.3 platform

- Build a clean 26.7.3 CSI-IAM image without core source patches.
- Port/replace/retire every CSI extension with source, service-loader registration, Jakarta imports, dependency review, and automated tests.
- Import/migrate a non-production realm and verify login, logout, refresh, MFA, themes, existing PHR profile calls, and required admin flows.
- Enable and test Keycloak Organizations, native Organization groups and role mappings, the selector authenticator, the membership and `organization_roles` mappers, client scope, audience, and ordinary-group tenant-wide administrator path.

### Phase 2 — authorization service and PHR backend

- Deploy POC-derived Cerbos, ADS, authorization data store, invalidation transport, policy release process, and administration service.
- Build the PHR resource catalog, policies, target resolvers, and PEP integrations.
- Seed role mappings only after legacy-permission transformation is reviewed and reconciled.
- Verify that Keycloak/identity-directory outages do not sit on the ADS warm decision path.

### Phase 3 — shared UI libraries and PHR migration

- Upgrade PHR and CSI UI libraries to the approved maintained Angular baseline.
- Publish the shared capability library through public library exports.
- Integrate modern auth and organization-scoped token switching.
- Migrate PHR progressively by route/workflow, removing the legacy screen and business permission checks only after equivalent PEPs and capability rendering are live.

### Phase 4 — controlled cutover

- Run dual-read comparison telemetry only where it does not allow the old result to bypass the new PEP.
- Reconcile users, native Organization-group assignments, roles, organizations, resource attributes, mapped permissions, and revisions.
- Release the 26.7.3 server, provider, schema, mapper configuration, and PHR claim consumer together; invalidate all user, offline, and SSO sessions at cutover.
- Cut over one PHR workflow/hospital/tenant cohort at a time with rollback checkpoints.
- Remove compatibility façades, old permission caches, and retired Keycloak extensions after all consumers migrate.

### Minimum acceptance tests

1. A user can only receive a PHR token for an organization they are a Keycloak member of.
2. A hospital switch replaces the token, clears snapshots/local PHR context, and loads data only for the newly active hospital.
3. A token from an unregistered realm, wrong audience, expired key, or `sys:` reserved role is refused.
4. ADS canonical runtime roles exactly match roles returned by the Identity Directory for the same user.
5. A role-matrix change updates PHR rendering after the expected invalidation/revision path, without a Cerbos policy release.
6. A user revoke defeats a role grant; a mandatory hospital/tenant rule defeats both.
7. Direct API calls remain denied even when a browser modifies a guard, capability cache, local storage, request body, or `x-hospital` header.
8. An instance capability receives only server-loaded PHR resource attributes; a caller cannot relabel a resource with another tenant/hospital.
9. PHR list screens use batch row capability results and do not create N browser-to-ADS decision requests.
10. An authorization change is auditable end to end: correlation ID, PHR operation, ADS/Cerbos call ID, authorization revision, and actor are available without exposing clinical data.

## 9. Open decisions that block policy authoring

1. Which existing CSI-IAM 26.7.3 branch/repository, if any, is authoritative? The checked-in CSI-IAM and extension source both currently identify themselves as 6.0.1.
2. Is a CSI hospital exactly a Keycloak Organization, or does the legacy hierarchy represent additional levels that must remain outside Keycloak?
3. Which PHR operations and regulated workflows require actions beyond the POC's generated FHIR action vocabulary?
4. What are the authoritative PHR services/tables for every resource's tenant, hospital, status, controlled-medication state, and other mandatory policy attributes?
5. What is the relationship between an authorization hospital and PHR's selected pharmacy/dispensing location?
6. Which CSI extension REST APIs must remain for other applications, and for how long can a compatibility façade exist?
7. Which database platforms and HA/cache/Kafka topology must the ADS support in each deployment environment?
8. Who owns the PHR policy repository, capability catalog approval, role-assignment migration, and policy-release operational runbook?

## 10. Primary evidence

- Cerbos architecture, trust boundaries, identity model, policy precedence, PEP, and Angular capability contract: `docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md`.
- POC Keycloak provider, Organizations realm configuration, and container build: `apps/keycloak-org-selector/`, `deploy/keycloak/realm-tenant-a.json`, and `docker-compose.yml`.
- POC verified token contract: `libs/tokenverifier/tokenverifier.go`.
- POC capability snapshot implementation: `apps/ads/internal/capability/capability.go` and `libs/web/capability/`.
- POC Keycloak Admin REST identity adapter: `libs/idpdirectory/keycloak/keycloak.go`.
- PHR auth/config/guard integration: `../../../Pharmacy/phr-pharmacygui/src/app/app.module.ts`, `../../../Pharmacy/phr-pharmacygui/src/app/app-routing.module.ts`, and `../../../Pharmacy/phr-pharmacygui/src/app/_guard/baseutil.guard.ts`.
- PHR legacy permission model: `../../../Pharmacy/phr-pharmacygui/PermissionModule.json` and the installed `@csi/csi-security-appmenu` type/API declarations.
- CSI-IAM legacy server build/custom core changes: `../../../Security-new/CSI-IAM/Newiam/csi-iam-service/Dockerfile`, `../../../Security-new/CSI-IAM/Newiam/csi-iam-service/server/tools/`, `../../../Security-new/CSI-IAM/Newiam/csi-iam-service/server-spi*/`, and `../../../Security-new/CSI-IAM/Newiam/csi-iam-service/model/*/`.
- CSI extension source and extension APIs: `../../../Security-new/csi-iam-extentions-v2/`.
