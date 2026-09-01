package org.cerbospoc.keycloak.orgselector;

import java.util.List;
import java.util.Optional;
import java.util.stream.Collectors;
import org.keycloak.OAuth2Constants;
import org.keycloak.authentication.AuthenticationFlowContext;
import org.keycloak.models.UserModel;
import org.keycloak.organization.OrganizationProvider;

/**
 * The Keycloak-facing half of organization selection (issue #79): reads the
 * facts {@link OrganizationSelectionDecision} needs from a real session, and
 * applies a {@link OrganizationSelectionDecision.Selected} outcome back onto
 * it. Kept separate from the authenticator itself so the one class that
 * actually touches Keycloak's session API is as small as possible.
 */
final class OrganizationSupport {

    private OrganizationSupport() {
    }

    /** Every organization alias the user belongs to, in no particular order. */
    static List<String> membershipAliases(AuthenticationFlowContext context, UserModel user) {
        OrganizationProvider organizations = context.getSession().getProvider(OrganizationProvider.class);
        return organizations.getByMember(user)
                .map(organization -> organization.getAlias())
                .collect(Collectors.toList());
    }

    /**
     * The organization alias already named in the request's own
     * {@code scope} parameter (e.g. {@code organization:north-hospital}),
     * if any - read from the auth session's own client note, which is
     * where Keycloak's OAuth2 layer already carries the requested scope
     * (§75's spike confirms Keycloak reads this same shape for a direct
     * grant; the browser flow's auth session carries it identically).
     */
    static Optional<String> requestedAlias(AuthenticationFlowContext context) {
        return organizationScopeValue(context.getAuthenticationSession().getClientNote(OAuth2Constants.SCOPE));
    }

    private static Optional<String> organizationScopeValue(String scopeParam) {
        if (scopeParam == null || scopeParam.isBlank()) {
            return Optional.empty();
        }
        for (String scope : scopeParam.split("\\s+")) {
            if (scope.startsWith(ORGANIZATION_SCOPE_PREFIX) && scope.length() > ORGANIZATION_SCOPE_PREFIX.length()) {
                return Optional.of(scope.substring(ORGANIZATION_SCOPE_PREFIX.length()));
            }
        }
        return Optional.empty();
    }

    private static final String ORGANIZATION_SCOPE_PREFIX = "organization:";

    /**
     * Records alias as the session's selected organization by making it
     * exactly what a caller who requested it in scope would have produced:
     * present in the auth session's own {@code scope} client note, the same
     * note {@link #requestedAlias} reads and the same shape §75's spike
     * confirmed Keycloak's own token-minting pipeline honours. This is
     * deliberately not a second, bespoke mechanism - the fewer places that
     * decide "which organization won", the fewer places that can disagree.
     */
    static void selectOrganization(AuthenticationFlowContext context, String alias) {
        String scopeParam = context.getAuthenticationSession().getClientNote(OAuth2Constants.SCOPE);
        if (organizationScopeValue(scopeParam).isPresent()) {
            return;
        }
        String updated = (scopeParam == null || scopeParam.isBlank())
                ? ORGANIZATION_SCOPE_PREFIX + alias
                : scopeParam + " " + ORGANIZATION_SCOPE_PREFIX + alias;
        context.getAuthenticationSession().setClientNote(OAuth2Constants.SCOPE, updated);
    }
}
