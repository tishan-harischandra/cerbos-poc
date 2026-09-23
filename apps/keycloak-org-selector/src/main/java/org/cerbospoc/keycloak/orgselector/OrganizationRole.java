package org.cerbospoc.keycloak.orgselector;

public record OrganizationRole(Kind kind, String clientId, String name) {
    public enum Kind { REALM, CLIENT }

    public static OrganizationRole realm(String name) {
        return new OrganizationRole(Kind.REALM, null, name);
    }

    public static OrganizationRole client(String clientId, String name) {
        return new OrganizationRole(Kind.CLIENT, clientId, name);
    }
}
