package org.cerbospoc.keycloak.orgselector;

import static org.junit.jupiter.api.Assertions.assertEquals;

import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class OrganizationRolesClaimTest {

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

    @Test
    void mapperContractIsStable() {
        assertEquals("cerbos-poc-organization-roles-mapper", OrganizationRolesMapper.PROVIDER_ID);
        assertEquals("organization_roles", OrganizationRolesMapper.CLAIM_NAME);
        assertEquals(10, new OrganizationRolesMapper().getPriority(),
                "organization roles must run after Keycloak resolves the active organization");
    }
}
