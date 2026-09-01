package org.cerbospoc.keycloak.orgselector;

/**
 * The realm role names organization selection reads. A separate, named
 * constant rather than a literal inline: {@code libs/tokenverifier}'s
 * {@code TenantWideRealmRole} names the identical role from the Go side of
 * this same contract (issue #78), and the two must never drift apart.
 */
final class OrganizationSelectionRealmRoles {

    /** The tenant-wide marker: a token with no active organization is still usable. */
    static final String TENANT_WIDE = "admin";

    private OrganizationSelectionRealmRoles() {
    }
}
