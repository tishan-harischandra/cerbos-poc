package org.cerbospoc.keycloak.orgselector;

import java.util.Collection;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.SortedSet;
import java.util.TreeSet;

public final class OrganizationRolesClaim {

    private OrganizationRolesClaim() {
    }

    public static Map<String, Object> from(Collection<OrganizationRole> roles, String receivingClientId) {
        Objects.requireNonNull(roles, "roles");
        Objects.requireNonNull(receivingClientId, "receivingClientId");

        SortedSet<String> realmRoles = new TreeSet<>();
        SortedSet<String> clientRoles = new TreeSet<>();

        for (OrganizationRole role : roles) {
            Objects.requireNonNull(role, "role");
            if (role.name() == null || role.name().isBlank()) {
                throw new IllegalArgumentException("organization role name must not be blank");
            }

            if (role.kind() == OrganizationRole.Kind.REALM) {
                realmRoles.add(role.name());
            } else if (role.kind() == OrganizationRole.Kind.CLIENT
                    && receivingClientId.equals(role.clientId())) {
                clientRoles.add(role.name());
            }
        }

        Map<String, Object> claim = new LinkedHashMap<>();
        if (!realmRoles.isEmpty()) {
            claim.put("realm", List.copyOf(realmRoles));
        }
        if (!clientRoles.isEmpty()) {
            claim.put("client", Map.of(receivingClientId, List.copyOf(clientRoles)));
        }
        return claim;
    }
}
