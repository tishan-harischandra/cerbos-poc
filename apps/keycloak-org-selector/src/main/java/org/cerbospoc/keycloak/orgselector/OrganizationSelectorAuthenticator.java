package org.cerbospoc.keycloak.orgselector;

import jakarta.ws.rs.core.Response;
import java.util.List;
import java.util.Optional;
import org.jboss.logging.Logger;
import org.keycloak.authentication.AuthenticationFlowContext;
import org.keycloak.authentication.AuthenticationFlowError;
import org.keycloak.authentication.Authenticator;
import org.keycloak.models.KeycloakSession;
import org.keycloak.models.RealmModel;
import org.keycloak.models.RoleModel;
import org.keycloak.models.UserModel;

/**
 * The login-flow half of organization selection (issue #79). Placed after
 * credentials and any second factor, it gathers the facts
 * {@link OrganizationSelectionDecision} needs from the authenticated
 * session and acts on the outcome:
 *
 * <ul>
 *   <li>{@link OrganizationSelectionDecision.Selected} - the organization is
 *       recorded and the flow proceeds with no screen shown;
 *   <li>{@link OrganizationSelectionDecision.Refused} - the flow fails with
 *       an explicit reason rather than a generic authentication failure;
 *   <li>{@link OrganizationSelectionDecision.Undecided} - this
 *       authenticator has no basis to decide (the selection screen slice
 *       does), so the flow proceeds unchanged.
 * </ul>
 */
public class OrganizationSelectorAuthenticator implements Authenticator {

    private static final Logger LOG = Logger.getLogger(OrganizationSelectorAuthenticator.class);

    @Override
    public void authenticate(AuthenticationFlowContext context) {
        RealmModel realm = context.getRealm();
        UserModel user = context.getUser();

        List<String> memberships = OrganizationSupport.membershipAliases(context, user);
        boolean isAdministrator = isTenantWideAdministrator(realm, user);
        Optional<String> requestedAlias = OrganizationSupport.requestedAlias(context);

        OrganizationSelectionDecision.Outcome outcome =
                OrganizationSelectionDecision.decide(memberships, isAdministrator, requestedAlias);

        if (outcome instanceof OrganizationSelectionDecision.Selected selected) {
            OrganizationSupport.selectOrganization(context, selected.alias());
            LOG.debugf("organization selector: %s selected for %s", selected.alias(), user.getUsername());
            context.success();
            return;
        }

        if (outcome instanceof OrganizationSelectionDecision.Refused refused) {
            LOG.infof("organization selector: refusing %s: %s", user.getUsername(), refused.reason());
            Response challenge = context.form()
                    .setError(refused.reason())
                    .createErrorPage(Response.Status.FORBIDDEN);
            context.failure(AuthenticationFlowError.ACCESS_DENIED, challenge);
            return;
        }

        // Undecided: more than one membership, or an administrator - the
        // selection screen a later slice adds is what decides these, not
        // this authenticator. Proceeding unchanged is the deliberate
        // no-op, not an oversight.
        context.success();
    }

    /**
     * The tenant-wide marker (issue #78) is the realm role named
     * {@link OrganizationSelectionRealmRoles#TENANT_WIDE}. A realm that has
     * never declared the role has no administrators by definition, so a
     * missing role is not an error here - it is simply "no one is one".
     */
    private static boolean isTenantWideAdministrator(RealmModel realm, UserModel user) {
        RoleModel role = realm.getRole(OrganizationSelectionRealmRoles.TENANT_WIDE);
        return role != null && user.hasRole(role);
    }

    @Override
    public void action(AuthenticationFlowContext context) {
        // No form is ever submitted back to this authenticator: it either
        // decides silently or fails the flow outright in authenticate().
    }

    @Override
    public boolean requiresUser() {
        return true;
    }

    @Override
    public boolean configuredFor(KeycloakSession session, RealmModel realm, UserModel user) {
        return true;
    }

    @Override
    public void setRequiredActions(KeycloakSession session, RealmModel realm, UserModel user) {
        // Nothing to set up: this authenticator only reads existing state.
    }

    @Override
    public void close() {
        // Stateless; nothing to release.
    }
}
