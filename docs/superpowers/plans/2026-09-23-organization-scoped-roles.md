# Organization-Scoped Roles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove that one user is a doctor in North Hospital and an auditor in South Hospital, with ADS authorizing exclusively from the active organization's Keycloak group roles.

**Architecture:** Upgrade Keycloak to 26.7.3 and use native organization groups for assignment. A custom OIDC mapper emits a strict `organization_roles` object; the Go verifier validates and canonicalizes only that claim for hospital sessions while preserving separately grouped tenant-wide administration.

**Tech Stack:** Keycloak 26.7.3 Organizations, Java 17 Keycloak SPI, Go 1.25, Bash/curl/jq E2E, Docker Compose/Podman, Nx, PostgreSQL load seeding

**Spec:** `docs/superpowers/specs/2026-09-23-organization-scoped-roles-design.md`

## Global Constraints

- Keycloak 26.7.3 is the single build, runtime, compose, Kubernetes, and load-test baseline.
- Native Keycloak organization groups are the source of hospital-specific role assignments.
- The access-token claim is `organization_roles: {"realm": string[], "client": {"<client-id>": string[]}}`.
- Hospital-scoped sessions never fall back to `realm_access` or `resource_access`.
- Tenant-wide administrators use ordinary Keycloak groups, never organization groups.
- The role matrix remains tenant + canonical role + resource/action; no hospital column is added.
- Missing, malformed, ambiguous, foreign-client, or reserved organization roles fail closed.
- Do not modify or stage the unrelated untracked architecture/diagram/test-result files already present in the worktree.
- Follow red-green TDD for every production behavior.

## File Structure

### New files

- `apps/keycloak-org-selector/src/main/java/org/cerbospoc/keycloak/orgselector/OrganizationRole.java` — provider-neutral value describing one realm or client role before claim rendering.
- `apps/keycloak-org-selector/src/main/java/org/cerbospoc/keycloak/orgselector/OrganizationRolesClaim.java` — pure deterministic claim builder.
- `apps/keycloak-org-selector/src/main/java/org/cerbospoc/keycloak/orgselector/OrganizationRolesMapper.java` — Keycloak session/group adapter and OIDC mapper.
- `apps/keycloak-org-selector/src/test/java/org/cerbospoc/keycloak/orgselector/OrganizationRolesClaimTest.java` — pure mapper contract tests.
- `scripts/keycloak-organization-role-seed.sh` — idempotent native organization-group/admin-group fixture seeding through Keycloak Admin REST.

### Modified files

- `apps/keycloak-org-selector/pom.xml` and `Dockerfile` — align provider and runtime with Keycloak 26.7.3.
- `apps/keycloak-org-selector/src/main/resources/META-INF/services/org.keycloak.protocol.ProtocolMapper` — register the new mapper.
- `docker-compose.yml`, `.env.example`, and `deploy/k8s/base/common/kustomization.yaml` — select `ORGANIZATION` roles and the 26.7.3 load-test image.
- `Makefile` — seed identity groups after Keycloak becomes healthy.
- `deploy/keycloak/realm-tenant-{a,b,c}.json` — configure the mapper; keep tenant A's global doctor fixture to prove it cannot leak.
- `libs/tokenverifier/tokenverifier.go` and `_test.go` — strict claim parsing, canonicalization, fail-closed behavior, and tenant-wide branch.
- `libs/idpdirectory/provider/provider.go` and `_test.go` — accept the organization role source and construct verifiers consistently.
- `libs/idpdirectory/keycloak/keycloak.go` and `_test.go` — list both realm roles and the configured client's roles when organization mode is selected.
- `scripts/tests/compose-contract.py` — assert the version and role-source deployment contract.
- `scripts/tests/org-selector-e2e.sh` — retain both selected tokens and prove role/decision isolation.
- `libs/keycloakbulkload/{keycloakbulkload.go,organizations.go,writer.go,writer_integration_test.go}` and `libs/keycloakbulkload/cmd/loadseed/main.go` — seed organization role groups and validate the 26.7.3 schema.
- `docs/MEASURED_FINDINGS.md`, `README.md`, and `docs/CERBOS_PHR_CSI_IAM_ADAPTATION_REQUIREMENTS.md` — replace obsolete 26.4/custom-hospital-role assumptions with measured 26.7.3 behavior.

---

### Task 1: Pin and contract-test Keycloak 26.7.3

**Files:**
- Modify: `scripts/tests/compose-contract.py:84-125`
- Modify: `apps/keycloak-org-selector/pom.xml:19-27`
- Modify: `apps/keycloak-org-selector/Dockerfile:21-27`
- Modify: `docker-compose.yml:84-104,157-163`

**Interfaces:**
- Consumes: existing compose topology and provider image build.
- Produces: one explicit `KEYCLOAK_VERSION = "26.7.3"` deployment contract and matching Maven/runtime pins.

- [ ] **Step 1: Add a failing version-alignment contract**

Add constants and checks to `scripts/tests/compose-contract.py` that read the Dockerfile, POM, and load-test image:

```python
KEYCLOAK_VERSION = "26.7.3"
KEYCLOAK_DOCKERFILE = REPO_ROOT / "apps" / "keycloak-org-selector" / "Dockerfile"
KEYCLOAK_POM = REPO_ROOT / "apps" / "keycloak-org-selector" / "pom.xml"


def check_keycloak_version_alignment(services: dict) -> None:
    dockerfile = KEYCLOAK_DOCKERFILE.read_text()
    pom = KEYCLOAK_POM.read_text()
    loadtest_image = services["keycloak-loadtest"]["image"]
    check("Keycloak runtime is pinned to 26.7.3", f"keycloak:{KEYCLOAK_VERSION}" in dockerfile)
    check("Keycloak SPI compiles against 26.7.3", f"<keycloak.version>{KEYCLOAK_VERSION}</keycloak.version>" in pom)
    check("load-test Keycloak is pinned to 26.7.3", loadtest_image.endswith(f":{KEYCLOAK_VERSION}"))
```

Call it from `main()` after loading `services`.

- [ ] **Step 2: Run the contract and confirm RED**

Run:

```bash
python3 scripts/tests/compose-contract.py
```

Expected: FAIL for the runtime, Maven, and load-test 26.7.3 checks because each is still pinned to 26.4/26.4.0.

- [ ] **Step 3: Update every executable version pin**

Set:

```xml
<keycloak.version>26.7.3</keycloak.version>
```

Use `quay.io/keycloak/keycloak:26.7.3` in both the custom image and `keycloak-loadtest`. Update adjacent version comments without adding unrelated documentation.

- [ ] **Step 4: Run the contract and provider image build**

Run:

```bash
python3 scripts/tests/compose-contract.py
docker build --target build -f apps/keycloak-org-selector/Dockerfile .
```

Expected: both commands exit 0; Maven compiles/tests against 26.7.3.

- [ ] **Step 5: Commit the version baseline**

```bash
git add scripts/tests/compose-contract.py apps/keycloak-org-selector/pom.xml apps/keycloak-org-selector/Dockerfile docker-compose.yml
git commit -m "Upgrade the identity baseline to Keycloak 26.7.3"
```

---

### Task 2: Build the structured organization-role mapper

**Files:**
- Create: `apps/keycloak-org-selector/src/main/java/org/cerbospoc/keycloak/orgselector/OrganizationRole.java`
- Create: `apps/keycloak-org-selector/src/main/java/org/cerbospoc/keycloak/orgselector/OrganizationRolesClaim.java`
- Create: `apps/keycloak-org-selector/src/main/java/org/cerbospoc/keycloak/orgselector/OrganizationRolesMapper.java`
- Create: `apps/keycloak-org-selector/src/test/java/org/cerbospoc/keycloak/orgselector/OrganizationRolesClaimTest.java`
- Modify: `apps/keycloak-org-selector/src/main/resources/META-INF/services/org.keycloak.protocol.ProtocolMapper`

**Interfaces:**
- Produces: `OrganizationRolesClaim.from(Collection<OrganizationRole> roles, String receivingClientId): Map<String,Object>`.
- Produces: OIDC provider ID `cerbos-poc-organization-roles-mapper` and claim name `organization_roles`.
- Consumes: `OrganizationProvider.getOrganizationGroupsByMember`, `GroupModel.getRoleMappingsStream`, and `RoleUtils.expandCompositeRoles` from Keycloak 26.7.3.

- [ ] **Step 1: Write failing pure claim-builder tests**

Create `OrganizationRolesClaimTest.java` with these concrete cases:

```java
@Test
void separatesRealmAndReceivingClientRoles() {
    Map<String, Object> claim = OrganizationRolesClaim.from(List.of(
        OrganizationRole.realm("doctor"),
        OrganizationRole.client("patient-app", "care-team"),
        OrganizationRole.client("reporting-app", "report-reader")
    ), "patient-app");

    assertEquals(List.of("doctor"), claim.get("realm"));
    assertEquals(Map.of("patient-app", List.of("care-team")), claim.get("client"));
}

@Test
void deduplicatesSortsAndOmitsEmptySections() {
    Map<String, Object> claim = OrganizationRolesClaim.from(List.of(
        OrganizationRole.realm("doctor"),
        OrganizationRole.realm("auditor"),
        OrganizationRole.realm("doctor")
    ), "patient-app");

    assertEquals(Map.of("realm", List.of("auditor", "doctor")), claim);
}

@Test
void emptyInputProducesAnEmptyObject() {
    assertEquals(Map.of(), OrganizationRolesClaim.from(List.of(), "patient-app"));
}
```

- [ ] **Step 2: Build and confirm RED**

Run:

```bash
docker build --target build -f apps/keycloak-org-selector/Dockerfile .
```

Expected: compilation failure because `OrganizationRole` and `OrganizationRolesClaim` do not exist.

- [ ] **Step 3: Implement the pure role and claim types**

Implement:

```java
public record OrganizationRole(Kind kind, String clientId, String name) {
    public enum Kind { REALM, CLIENT }
    public static OrganizationRole realm(String name) { return new OrganizationRole(Kind.REALM, null, name); }
    public static OrganizationRole client(String clientId, String name) { return new OrganizationRole(Kind.CLIENT, clientId, name); }
}
```

`OrganizationRolesClaim.from` must validate nonblank names, include only the receiving client, deduplicate with sorted sets, and return only nonempty `realm`/`client` sections.

- [ ] **Step 4: Run the pure tests and confirm GREEN**

Run the same Docker build. Expected: JUnit passes and the build stage exits 0.

- [ ] **Step 5: Add a failing mapper registration/build test**

Extend the JUnit suite with a provider identity assertion:

```java
@Test
void mapperContractIsStable() {
    assertEquals("cerbos-poc-organization-roles-mapper", OrganizationRolesMapper.PROVIDER_ID);
    assertEquals("organization_roles", OrganizationRolesMapper.CLAIM_NAME);
}
```

Run the Docker build. Expected: compilation failure because `OrganizationRolesMapper` does not exist.

- [ ] **Step 6: Implement the Keycloak mapper**

Implement `OrganizationRolesMapper` as an `AbstractOIDCProtocolMapper` and `OIDCAccessTokenMapper`. In `setClaim`:

```java
OrganizationModel active = keycloakSession.getContext().getOrganization();
if (active == null || !active.isMember(userSession.getUser())) return;
OrganizationProvider provider = keycloakSession.getProvider(OrganizationProvider.class);
Set<RoleModel> mapped = provider.getOrganizationGroupsByMember(active, userSession.getUser())
    .flatMap(GroupModel::getRoleMappingsStream)
    .collect(Collectors.toSet());
Set<RoleModel> expanded = RoleUtils.expandCompositeRoles(mapped);
```

Adapt realm roles to `OrganizationRole.realm`, roles owned by the receiving `ClientModel` to `OrganizationRole.client`, ignore other clients, and write even an empty object using:

```java
token.getOtherClaims().put(CLAIM_NAME,
    OrganizationRolesClaim.from(roles, clientSessionCtx.getClientSession().getClient().getClientId()));
```

Register both `OrganizationMembershipsMapper` and `OrganizationRolesMapper`, one fully qualified class name per line, in the service file.

- [ ] **Step 7: Build and confirm GREEN**

Run:

```bash
docker build --target build -f apps/keycloak-org-selector/Dockerfile .
```

Expected: all Java tests pass and the provider JAR is built.

- [ ] **Step 8: Commit the mapper**

```bash
git add apps/keycloak-org-selector/src/main apps/keycloak-org-selector/src/test
git commit -m "Emit active organization roles in a structured token claim"
```

---

### Task 3: Parse and canonicalize organization roles in ADS

**Files:**
- Modify: `libs/tokenverifier/tokenverifier.go:60-77,193-212,242-357`
- Modify: `libs/tokenverifier/tokenverifier_test.go`

**Interfaces:**
- Produces: `RoleSourceOrganization RoleSource = "ORGANIZATION"`.
- Produces: `OrganizationRolesClaim = "organization_roles"`.
- Produces: strict internal `organizationRoles` shape with `Realm []string` and `Client map[string][]string`.
- Consumes: the verified active hospital from `hospitalOf` and configured `ClientID`.

- [ ] **Step 1: Write RED tests for canonicalization and global-role isolation**

Add tests that create an organization-scoped verifier and signed claims:

```go
func TestOrganizationRolesAreCanonicalizedForTheVerifiedRealm(t *testing.T) {
    payload := fixture.valid(claims{
        "organization": []string{"north-hospital"},
        "organization_roles": map[string]any{
            "realm": []string{"doctor"},
            "client": map[string]any{"patient-app": []string{"care-team"}},
        },
        "realm_access": map[string]any{"roles": []string{"auditor"}},
    })
    verified, err := organizationVerifier(t, fixture).Verify(context.Background(), fixture.sign(t, payload))
    if err != nil { t.Fatalf("Verify: %v", err) }
    want := []string{"kc:tenant-a:realm:doctor", "kc:tenant-a:patient-app:care-team"}
    if !slices.Equal(verified.Roles, want) { t.Fatalf("Roles = %v, want %v", verified.Roles, want) }
}
```

Add table cases for absent claim, scalar/array claim, unknown field, non-string role, foreign client key, and reserved role.

- [ ] **Step 2: Run and confirm RED**

Run:

```bash
bash scripts/go.sh libs/tokenverifier test ./...
```

Expected: compilation failure for `RoleSourceOrganization` followed by behavior failures until parsing exists.

- [ ] **Step 3: Implement strict decoding and organization canonicalization**

Add `RoleSourceOrganization`. Reorder `Verify` so `hospitalOf(claims)` is resolved before role normalization, then call:

```go
roles, err := v.normaliseRoles(claims, hospital)
```

For a nonempty hospital and organization source:

- require the raw claim;
- decode with `json.Decoder.DisallowUnknownFields()` by re-marshaling only that claim;
- reject nil arrays, blank entries, foreign client keys, and invalid shapes;
- canonicalize realm roles with `canonicalid.KeycloakRealmRole(v.cfg.Realm, role)`;
- canonicalize the configured client's roles with `canonicalid.KeycloakClientRole(v.cfg.Realm, v.cfg.ClientID, role)`;
- sort and deduplicate the final canonical IDs.

For an empty hospital and valid tenant-wide marker, require the organization claim to be absent and normalize global realm roles plus only `resource_access[v.cfg.ClientID]` roles. Continue scanning every global and organization role for `sys:` before returning.

- [ ] **Step 4: Add RED tests for tenant-wide separation**

Add cases proving:

```go
// no organization + admin marker + no organization_roles => accepted
// no organization + admin marker + organization_roles => rejected
// active organization + global doctor + organization auditor => only auditor
// active organization + empty organization_roles => zero roles
```

Run the module tests and confirm the new cases fail for the intended missing branches.

- [ ] **Step 5: Complete the tenant-wide and reserved-role branches**

Introduce explicit errors such as `ErrMalformedOrganizationRoles` and `ErrOrganizationRolesScopeMismatch`, wrapping them with field context but never logging token contents.

- [ ] **Step 6: Run all token-verifier tests**

```bash
bash scripts/go.sh libs/tokenverifier test ./...
```

Expected: PASS with no warnings or failures.

- [ ] **Step 7: Commit the verifier contract**

```bash
git add libs/tokenverifier/tokenverifier.go libs/tokenverifier/tokenverifier_test.go
git commit -m "Resolve effective roles from the active organization claim"
```

---

### Task 4: Wire organization role mode through provider and directory adapters

**Files:**
- Modify: `libs/idpdirectory/provider/provider.go`
- Modify: `libs/idpdirectory/provider/provider_test.go`
- Modify: `libs/idpdirectory/keycloak/keycloak.go`
- Modify: `libs/idpdirectory/keycloak/keycloak_test.go`
- Modify: `.env.example`
- Modify: `docker-compose.yml:229-387`
- Modify: `deploy/k8s/base/common/kustomization.yaml`
- Modify: `scripts/tests/compose-contract.py`

**Interfaces:**
- Consumes: `tokenverifier.RoleSourceOrganization` from Task 3.
- Produces: deployed `IDP_ROLE_SOURCE=ORGANIZATION`.
- Produces: organization-mode directory catalog containing realm roles plus roles of the configured browser client, each with the existing canonical ID format.

- [ ] **Step 1: Write failing provider/config tests**

Add tests that set `IDP_ROLE_SOURCE=ORGANIZATION`, call `ConfigFromTenant`, build `NewVerifier`, and assert the mode and browser client survive unchanged. Add a compose-contract assertion:

```python
check("ADS uses organization-scoped token roles",
      environment.get("IDP_ROLE_SOURCE", "").endswith("ORGANIZATION}"))
```

Use a robust condition for the actual Compose interpolation string rather than matching unrelated text.

- [ ] **Step 2: Run and confirm RED**

```bash
bash scripts/go.sh libs/idpdirectory test ./...
python3 scripts/tests/compose-contract.py
```

Expected: provider construction or directory assumptions fail for the new mode, and compose still defaults to `REALM`.

- [ ] **Step 3: Make provider validation explicit**

Validate Keycloak role sources against exactly `CLIENT`, `REALM`, and `ORGANIZATION`; keep WSO2 behavior unchanged. Ensure `NewVerifier` passes the configured client ID for strict client-key validation.

- [ ] **Step 4: Add failing organization-mode directory tests**

Extend the fake Keycloak server with realm and client role endpoints. Assert `SearchRoles` in organization mode returns both:

```go
[]string{
    "kc:tenant-a:realm:doctor",
    "kc:tenant-a:patient-app:care-team",
}
```

Also assert `GetRole` preserves the role's source when constructing the canonical ID. Keep `GetUserRoles` defined as direct assignments only; organization-group-effective roles come from verified tokens, not a context-free user lookup.

- [ ] **Step 5: Implement source-aware directory aggregation**

Add focused helpers for realm and client role reads rather than extending `rolesPath` with implicit defaults. In organization mode, merge realm and configured-client pages before applying the external page window, deduplicate by canonical ID, and sort deterministically. Preserve the existing single-source fast paths for `REALM` and `CLIENT`.

- [ ] **Step 6: Switch deployment defaults**

Set `IDP_ROLE_SOURCE=ORGANIZATION` in `.env.example`, Compose defaults for ADS/admin/resource services, and the Kubernetes common environment. Do not change library defaults used by legacy/non-pilot configurations.

- [ ] **Step 7: Run focused and contract tests**

```bash
bash scripts/go.sh libs/idpdirectory test ./...
python3 scripts/tests/compose-contract.py
```

Expected: PASS.

- [ ] **Step 8: Commit provider wiring**

```bash
git add libs/idpdirectory .env.example docker-compose.yml deploy/k8s/base/common/kustomization.yaml scripts/tests/compose-contract.py
git commit -m "Select organization-scoped roles for pilot deployments"
```

---

### Task 5: Seed native organization groups and tenant-admin groups

**Files:**
- Create: `scripts/keycloak-organization-role-seed.sh`
- Modify: `Makefile:19-35,51-68`
- Modify: `deploy/keycloak/realm-tenant-a.json`
- Modify: `deploy/keycloak/realm-tenant-b.json`
- Modify: `deploy/keycloak/realm-tenant-c.json`
- Modify: `scripts/tests/compose-contract.py`

**Interfaces:**
- Consumes: provider ID `cerbos-poc-organization-roles-mapper`.
- Produces: idempotent Admin REST setup for organization groups, role mappings, and memberships.
- Produces: `make seed-idp-roles`, called after Keycloak health and before application services start.

- [ ] **Step 1: Add failing realm/compose contracts**

Extend `check_identity_provider` to require the `patient-app` mapper:

```python
mappers = {m["protocolMapper"] for m in clients["patient-app"].get("protocolMappers", [])}
check("patient-app emits active organization roles",
      "cerbos-poc-organization-roles-mapper" in mappers)
```

Also parse the Makefile and assert `up` and `up-tls` invoke `seed-idp-roles` after the initial Keycloak health wait.

- [ ] **Step 2: Run and confirm RED**

```bash
python3 scripts/tests/compose-contract.py
```

Expected: missing mapper and seed target failures.

- [ ] **Step 3: Configure the mapper in every pilot browser client**

Add this mapper to `patient-app` in tenant A/B/C fixtures:

```json
{
  "name": "organization-roles",
  "protocol": "openid-connect",
  "protocolMapper": "cerbos-poc-organization-roles-mapper",
  "config": { "access.token.claim": "true" }
}
```

Keep `user-doctor-multi`'s direct global `doctor` mapping in tenant A as the explicit negative leakage fixture.

- [ ] **Step 4: Implement the idempotent Admin REST seed script**

The script must:

1. Obtain the master admin token.
2. Resolve realm roles and users by exact name.
3. Resolve each organization by exact alias.
4. `GET /admin/realms/{realm}/organizations/{org-id}/groups` and create a missing group with `POST`.
5. Map the exact realm role through `/organizations/{org-id}/groups/{group-id}/role-mappings/realm`.
6. Add an existing organization member through `PUT /organizations/{org-id}/groups/{group-id}/members/{user-id}`; treat 204 and already-member 409 as success only after a confirming GET.
7. Create ordinary `tenant-admins` through the realm groups API, map `admin` and `administrator`, and add `user-admin` and `user-admin-clinician`.

Tenant A assignments must include:

```text
north-hospital / Doctors -> doctor -> user-doctor, user-doctor-multi,
  user-doctor-revoked, user-admin-clinician
north-hospital / Auditors -> auditor -> user-auditor
north-hospital / Clerks -> clerk -> user-clerk-granted
south-hospital / Auditors -> auditor -> user-doctor-multi
```

Seed equivalent single-role groups for tenant B/C fixture users so existing smoke tests continue to receive nonempty organization roles.

- [ ] **Step 5: Wire seeding into Make targets**

Add:

```make
.PHONY: seed-idp-roles
seed-idp-roles:
	bash scripts/keycloak-organization-role-seed.sh
```

Invoke it after the infrastructure wait in both `up` and `up-tls`, before ADS starts.

- [ ] **Step 6: Run contracts and a clean realm import**

Because existing imported realms are not updated by `--import-realm`, use a fresh Compose project name rather than deleting the current volume:

```bash
python3 scripts/tests/compose-contract.py
COMPOSE_PROJECT_NAME=cerbos-poc-orgroles-test docker compose up --build --detach postgres keycloak cerbos redpanda
COMPOSE_PROJECT_NAME=cerbos-poc-orgroles-test bash scripts/compose-wait.sh postgres keycloak cerbos redpanda
COMPOSE_PROJECT_NAME=cerbos-poc-orgroles-test make seed-idp-roles
```

Expected: seed succeeds twice consecutively and group/member queries return the exact assignments.

- [ ] **Step 7: Stop the temporary project without deleting the user's existing stack**

```bash
COMPOSE_PROJECT_NAME=cerbos-poc-orgroles-test docker compose down --remove-orphans --volumes
```

This removes only the temporary project created in Step 6.

- [ ] **Step 8: Commit identity fixtures**

```bash
git add scripts/keycloak-organization-role-seed.sh Makefile deploy/keycloak/realm-tenant-a.json deploy/keycloak/realm-tenant-b.json deploy/keycloak/realm-tenant-c.json scripts/tests/compose-contract.py
git commit -m "Seed hospital roles through native organization groups"
```

---

### Task 6: Prove different roles and decisions for the same user end to end

**Files:**
- Modify: `scripts/tests/org-selector-e2e.sh:288-365`
- Modify: `scripts/tests/identity-e2e.sh:50-105`

**Interfaces:**
- Consumes: North/South tokens from the existing real browser-code flow.
- Produces: release-blocker proof that North has doctor/allow and South has auditor/deny for `user-doctor-multi`.

- [ ] **Step 1: Preserve both real tokens and write failing role assertions**

In the existing two-answer section, retain `south_token` and `north_token`, then assert:

```bash
north_roles="$(claim_of "${north_token}" '.organization_roles.realm | sort | tojson')"
south_roles="$(claim_of "${south_token}" '.organization_roles.realm | sort | tojson')"
[[ "${north_roles}" == '["doctor"]' ]] || fail "north selects only doctor"
[[ "${south_roles}" == '["auditor"]' ]] || fail "south selects only auditor"
```

Also assert `.realm_access.roles` still contains the global `doctor` fixture in the South token, making the no-fallback test meaningful.

- [ ] **Step 2: Run against the new clean stack and confirm RED**

```bash
bash scripts/tests/org-selector-e2e.sh
```

Expected: role assertions fail until mapper configuration/group seeding and verifier changes are active.

- [ ] **Step 3: Add paired ADS decision assertions**

Post equivalent requests with matching hospital attributes:

```bash
north_request='{"resources":[{"kind":"patient_record","id":"north-proof","attributes":{"tenantId":"tenant-a","hospitalId":"north-hospital","status":"ACTIVE"},"actions":["read"]}]}'
south_request='{"resources":[{"kind":"patient_record","id":"south-proof","attributes":{"tenantId":"tenant-a","hospitalId":"south-hospital","status":"ACTIVE"},"actions":["read"]}]}'
```

Assert North is allowed by the live doctor grant and South is denied by the expired auditor grant. The South assertion simultaneously proves the global doctor role is ignored.

- [ ] **Step 4: Update identity claim assertions**

Change `identity-e2e.sh` from checking `realm_access.roles` as the authoritative source to checking `organization_roles.realm`. Retain a separate adversarial assertion that global claims do not affect hospital decisions.

- [ ] **Step 5: Run focused E2E GREEN**

```bash
bash scripts/tests/org-selector-e2e.sh
bash scripts/tests/identity-e2e.sh
```

Expected: both pass, explicitly reporting North doctor/allow and South auditor/deny.

- [ ] **Step 6: Commit the pilot proof**

```bash
git add scripts/tests/org-selector-e2e.sh scripts/tests/identity-e2e.sh
git commit -m "Prove one user receives hospital-specific roles end to end"
```

---

### Task 7: Adapt the 26.7.3 bulk loader to organization role groups

**Files:**
- Modify: `libs/keycloakbulkload/organizations.go`
- Modify: `libs/keycloakbulkload/keycloakbulkload.go`
- Modify: `libs/keycloakbulkload/writer.go`
- Modify: `libs/keycloakbulkload/writer_integration_test.go`
- Modify: `libs/keycloakbulkload/cmd/loadseed/main.go`
- Modify: `docs/MEASURED_FINDINGS.md`

**Interfaces:**
- Produces: `OrganizationRoleGroupIDs(ctx, pool, realmID) map[string]map[string]string`, keyed by organization alias then role name.
- Extends: `UserRecord` with `OrganizationRoleGroupIDs []string` in addition to backing organization memberships.
- Consumes: native 26.7.3 organization subgroup and role-mapping rows created through Admin REST.

- [ ] **Step 1: Upgrade the integration test's contract and write RED assertions**

Change the test description to 26.7.3. In `TestBulkLoadedUsersCarryOrganizationMembership`, create organization groups with doctor/auditor mappings through Admin REST, bulk-load one user into different role groups, request each organization-scoped token, and assert:

```go
north := claims["organization_roles"].(map[string]any)
// north realm roles exactly [doctor]
south := claims["organization_roles"].(map[string]any)
// south realm roles exactly [auditor]
```

- [ ] **Step 2: Run the integration test and confirm RED**

```bash
docker compose --profile loadtest up --detach keycloak-db keycloak-loadtest
GO_NETWORK=cerbos-poc_default \
KEYCLOAK_LOADTEST_ADMIN_URL=http://keycloak-loadtest:8080 \
KEYCLOAK_LOADTEST_DB_DSN='postgres://keycloak:change-me@keycloak-db:5432/keycloak?sslmode=disable' \
GO_ENV_PASS='KEYCLOAK_LOADTEST_ADMIN_URL KEYCLOAK_LOADTEST_DB_DSN' \
bash scripts/go.sh libs/keycloakbulkload test ./... -run TestBulkLoadedUsersCarryOrganizationMembership -count=1
```

Expected: the new role assertion fails because users join only backing organization groups. If `.env` changes `COMPOSE_PROJECT_NAME`, substitute that exact value for `cerbos-poc` in `GO_NETWORK`; do not publish the database to the host.

- [ ] **Step 3: Add explicit organization-role group IDs to load records**

Extend `UserRecord`:

```go
OrganizationRoleGroupIDs []string
```

Update `writeBatch` to COPY both backing organization group memberships and organization role subgroup memberships into `USER_GROUP_MEMBERSHIP`, deduplicated per user. Update `LoadStats.Memberships` to count both sets.

- [ ] **Step 4: Discover and validate the 26.7.3 schema**

Implement `OrganizationRoleGroupIDs` using the 26.7.3 relationship between `ORG`, the internal organization group, child `KEYCLOAK_GROUP` rows, and role mappings. The query must constrain every group by realm and organization parent, never infer ownership from a display name alone. Add an integration assertion that every returned group has `GroupModel.Type.ORGANIZATION` when read back through Admin REST.

- [ ] **Step 5: Assign load-model roles per organization**

In `cmd/loadseed/main.go`, map each generated user's per-hospital role set to organization-role group IDs. Stop relying on global `RoleIDs` for pilot authorization; retain only roles required by non-pilot compatibility tests.

- [ ] **Step 6: Run unit and integration GREEN**

```bash
bash scripts/go.sh libs/keycloakbulkload test ./...
make loadtest-seed PROFILE=demo
```

Expected: the integration token has different roles per organization and the demo seed completes successfully.

- [ ] **Step 7: Record measured schema evidence**

Replace the 26.4-only organization-schema finding with the exact 26.7.3 tables/columns and integration command observed in Steps 4-6. Do not extrapolate unmeasured full-profile timing.

- [ ] **Step 8: Commit load-seed compatibility**

```bash
git add libs/keycloakbulkload docs/MEASURED_FINDINGS.md
git commit -m "Seed organization role groups in the Keycloak load model"
```

---

### Task 8: Run full verification and align operator documentation

**Files:**
- Modify: `README.md:31-205`
- Modify: `docs/CERBOS_PHR_CSI_IAM_ADAPTATION_REQUIREMENTS.md`
- Modify: any version comments found by the final stale-version scan

**Interfaces:**
- Consumes: all prior tasks.
- Produces: pilot-ready documentation and fresh verification evidence.

- [ ] **Step 1: Update documentation contracts**

Document:

- Keycloak 26.7.3 requirement;
- organization group assignment source;
- exact `organization_roles` JSON shape;
- `user-doctor-multi` North doctor/South auditor walkthrough;
- tenant-wide ordinary admin groups;
- no global-role fallback;
- coordinated upgrade/session invalidation requirement.

Replace adaptation text that says a third `hospital_roles` map source must be ported; the approved contract is now the custom structured claim derived from native organization groups.

- [ ] **Step 2: Scan for stale executable 26.4 assumptions**

Use the repository search tool for `26.4`, `IDP_ROLE_SOURCE=REALM`, and claims that roles always come from `realm_access`. Classify historical measured findings as historical rather than rewriting evidence; update executable/current-state statements only.

- [ ] **Step 3: Run all focused test suites**

```bash
docker build --target build -f apps/keycloak-org-selector/Dockerfile .
bash scripts/go.sh libs/tokenverifier test ./...
bash scripts/go.sh libs/idpdirectory test ./...
bash scripts/go.sh libs/keycloakbulkload test ./...
python3 scripts/tests/compose-contract.py
```

Expected: all exit 0.

- [ ] **Step 4: Rebuild a clean complete stack**

Use a separate project name so verification does not destroy the user's existing data:

```bash
COMPOSE_PROJECT_NAME=cerbos-poc-orgroles-final make up
```

Expected: all eight default services become healthy and identity group seeding is idempotent.

- [ ] **Step 5: Run complete smoke and walkthrough verification**

```bash
COMPOSE_PROJECT_NAME=cerbos-poc-orgroles-final make smoke
COMPOSE_PROJECT_NAME=cerbos-poc-orgroles-final make walkthrough
```

Expected: every suite passes, including the North doctor/South auditor decision proof.

- [ ] **Step 6: Run repository CI verification**

```bash
make ci
```

Expected: lint, test, build, generation drift, policy, architecture, compose, and Kubernetes validation all pass.

- [ ] **Step 7: Stop only the temporary verification stack**

```bash
COMPOSE_PROJECT_NAME=cerbos-poc-orgroles-final make down
```

Do not run `make clean`; it would remove volumes/images beyond this verification need.

- [ ] **Step 8: Review the complete diff and commit documentation**

```bash
git diff --check
git status --short
git diff --stat
git add README.md docs/CERBOS_PHR_CSI_IAM_ADAPTATION_REQUIREMENTS.md
git commit -m "Document organization-scoped roles for the pilot"
```

- [ ] **Step 9: Final acceptance check**

Confirm the verification log contains all of these explicit facts:

```text
North token: organization=north-hospital, organization_roles.realm=[doctor]
North patient_record:read: allowed=true
South token: organization=south-hospital, organization_roles.realm=[auditor]
South global realm_access still includes doctor
South patient_record:read: allowed=false
All default containers: healthy
make ci: exit 0
```

If any fact is absent, the pilot blocker remains open even if unit tests pass.
