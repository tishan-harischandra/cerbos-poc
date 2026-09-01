package org.cerbospoc.keycloak.orgselector;

import java.util.List;
import java.util.stream.Collectors;
import org.keycloak.models.ClientSessionContext;
import org.keycloak.models.KeycloakSession;
import org.keycloak.models.ProtocolMapperModel;
import org.keycloak.models.UserSessionModel;
import org.keycloak.organization.OrganizationProvider;
import org.keycloak.protocol.oidc.mappers.AbstractOIDCProtocolMapper;
import org.keycloak.protocol.oidc.mappers.OIDCAccessTokenMapper;
import org.keycloak.provider.ProviderConfigProperty;
import org.keycloak.representations.IDToken;

/**
 * Emits {@code organization_memberships} (issue #84): every organization
 * alias the user belongs to, regardless of which one the session made
 * active. Unlike Keycloak's own {@code organization} claim - populated only
 * for the organization named in the request's scope - this is not a
 * built-in claim, which is exactly why the hospital switcher needs its own
 * mapper: the switcher has to know what else is available to switch to.
 *
 * <p>The claim carries display data only. {@code tests/architecture}'s
 * {@code otherhospitals.go} check is what keeps a decision path from ever
 * reading the token field this claim ends up populating.
 */
public class OrganizationMembershipsMapper extends AbstractOIDCProtocolMapper implements OIDCAccessTokenMapper {

    public static final String PROVIDER_ID = "cerbos-poc-organization-memberships-mapper";

    /**
     * Matches {@code tokenverifier.OrganizationMembershipsClaim} on the Go
     * side (issue #84) - the two names are a contract, not a coincidence.
     */
    static final String CLAIM_NAME = "organization_memberships";

    @Override
    public String getDisplayCategory() {
        return TOKEN_MAPPER_CATEGORY;
    }

    @Override
    public String getDisplayType() {
        return "Organization memberships";
    }

    @Override
    public String getHelpText() {
        return "Every organization the user belongs to, for a hospital switcher; "
                + "never the active hospital alone.";
    }

    @Override
    public String getId() {
        return PROVIDER_ID;
    }

    @Override
    public List<ProviderConfigProperty> getConfigProperties() {
        return List.of();
    }

    @Override
    protected void setClaim(IDToken token, ProtocolMapperModel mappingModel, UserSessionModel userSession,
            KeycloakSession keycloakSession, ClientSessionContext clientSessionCtx) {
        OrganizationProvider organizations = keycloakSession.getProvider(OrganizationProvider.class);
        List<String> aliases = organizations.getByMember(userSession.getUser())
                .map(organization -> organization.getAlias())
                .collect(Collectors.toList());
        token.getOtherClaims().put(CLAIM_NAME, aliases);
    }
}
