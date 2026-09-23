package org.cerbospoc.keycloak.orgselector;

import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import java.util.stream.Collectors;
import org.keycloak.models.ClientModel;
import org.keycloak.models.ClientSessionContext;
import org.keycloak.models.GroupModel;
import org.keycloak.models.KeycloakSession;
import org.keycloak.models.OrganizationModel;
import org.keycloak.models.ProtocolMapperModel;
import org.keycloak.models.RoleModel;
import org.keycloak.models.UserSessionModel;
import org.keycloak.models.utils.RoleUtils;
import org.keycloak.organization.OrganizationProvider;
import org.keycloak.protocol.oidc.mappers.AbstractOIDCProtocolMapper;
import org.keycloak.protocol.oidc.mappers.OIDCAccessTokenMapper;
import org.keycloak.provider.ProviderConfigProperty;
import org.keycloak.representations.IDToken;

public class OrganizationRolesMapper extends AbstractOIDCProtocolMapper implements OIDCAccessTokenMapper {

    public static final String PROVIDER_ID = "cerbos-poc-organization-roles-mapper";
    public static final String CLAIM_NAME = "organization_roles";

    @Override
    public String getDisplayCategory() {
        return TOKEN_MAPPER_CATEGORY;
    }

    @Override
    public String getDisplayType() {
        return "Organization roles";
    }

    @Override
    public String getHelpText() {
        return "Realm and receiving-client roles attached to the active organization.";
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
        OrganizationModel active = keycloakSession.getContext().getOrganization();
        if (active == null || !active.isMember(userSession.getUser())) {
            return;
        }

        OrganizationProvider provider = keycloakSession.getProvider(OrganizationProvider.class);
        Set<RoleModel> mapped = provider.getOrganizationGroupsByMember(active, userSession.getUser())
                .flatMap(GroupModel::getRoleMappingsStream)
                .collect(Collectors.toSet());
        Set<RoleModel> expanded = RoleUtils.expandCompositeRoles(mapped);

        ClientModel receivingClient = clientSessionCtx.getClientSession().getClient();
        List<OrganizationRole> roles = new ArrayList<>();
        for (RoleModel role : expanded) {
            if (!role.isClientRole()) {
                roles.add(OrganizationRole.realm(role.getName()));
            } else if (receivingClient.getId().equals(role.getContainerId())) {
                roles.add(OrganizationRole.client(receivingClient.getClientId(), role.getName()));
            }
        }

        token.getOtherClaims().put(CLAIM_NAME,
                OrganizationRolesClaim.from(roles, receivingClient.getClientId()));
    }
}
