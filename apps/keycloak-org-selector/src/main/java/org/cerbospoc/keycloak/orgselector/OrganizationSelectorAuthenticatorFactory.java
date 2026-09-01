package org.cerbospoc.keycloak.orgselector;

import java.util.List;
import org.keycloak.Config;
import org.keycloak.authentication.Authenticator;
import org.keycloak.authentication.AuthenticatorFactory;
import org.keycloak.models.AuthenticationExecutionModel;
import org.keycloak.models.KeycloakSession;
import org.keycloak.models.KeycloakSessionFactory;
import org.keycloak.provider.ProviderConfigProperty;

/** Registers {@link OrganizationSelectorAuthenticator} as a login flow step (issue #79). */
public class OrganizationSelectorAuthenticatorFactory implements AuthenticatorFactory {

    public static final String PROVIDER_ID = "cerbos-poc-organization-selector";

    private static final OrganizationSelectorAuthenticator SINGLETON = new OrganizationSelectorAuthenticator();

    @Override
    public String getId() {
        return PROVIDER_ID;
    }

    @Override
    public String getDisplayType() {
        return "Organization Selector (Cerbos POC)";
    }

    @Override
    public String getReferenceCategory() {
        return "organization";
    }

    @Override
    public boolean isConfigurable() {
        return false;
    }

    @Override
    public AuthenticationExecutionModel.Requirement[] getRequirementChoices() {
        return new AuthenticationExecutionModel.Requirement[] {
            AuthenticationExecutionModel.Requirement.REQUIRED,
            AuthenticationExecutionModel.Requirement.DISABLED,
        };
    }

    @Override
    public boolean isUserSetupAllowed() {
        return false;
    }

    @Override
    public String getHelpText() {
        return "Selects the caller's active organization from their memberships, the tenant-wide "
                + "realm role and the request's own scope - the cases that need no selection screen "
                + "(issue #79).";
    }

    @Override
    public List<ProviderConfigProperty> getConfigProperties() {
        return List.of();
    }

    @Override
    public Authenticator create(KeycloakSession session) {
        return SINGLETON;
    }

    @Override
    public void init(Config.Scope config) {
        // No configuration to read.
    }

    @Override
    public void postInit(KeycloakSessionFactory factory) {
        // No cross-provider wiring needed.
    }

    @Override
    public void close() {
        // Stateless; nothing to release.
    }
}
