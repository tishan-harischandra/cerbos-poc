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
 * The login-flow half of organization selection (issues #79 and #80).
 * Placed after credentials and any second factor, it gathers the facts
 * {@link OrganizationSelectionDecision} needs from the authenticated
 * session and acts on the outcome:
 *
 * <ul>
 *   <li>{@link OrganizationSelectionDecision.Selected} - the organization is
 *       recorded and the flow proceeds with no screen shown;
 *   <li>{@link OrganizationSelectionDecision.NeedsSelection} - the
 *       {@code select-organization.ftl} screen is shown, listing exactly
 *       the offered options; {@link #action} handles its submission;
 *   <li>{@link OrganizationSelectionDecision.Refused} - the flow fails with
 *       an explicit reason rather than a generic authentication failure;
 *   <li>{@link OrganizationSelectionDecision.Undecided} - this
 *       authenticator has no basis to decide (an administrator's own
 *       screen, a later slice's concern), so the flow proceeds unchanged.
 * </ul>
 */
public class OrganizationSelectorAuthenticator implements Authenticator {

    private static final Logger LOG = Logger.getLogger(OrganizationSelectorAuthenticator.class);
    static final String SELECTION_FORM = "select-organization.ftl";
    static final String SELECTED_FIELD = "organization";

    @Override
    public void authenticate(AuthenticationFlowContext context) {
        UserModel user = context.getUser();
        List<String> memberships = OrganizationSupport.membershipAliases(context, user);
        boolean isAdministrator = isTenantWideAdministrator(context.getRealm(), user);
        Optional<String> requestedAlias = OrganizationSupport.requestedAlias(context);

        act(context, user, memberships,
                OrganizationSelectionDecision.decide(memberships, isAdministrator, requestedAlias));
    }

    @Override
    public void action(AuthenticationFlowContext context) {
        UserModel user = context.getUser();
        // Re-derived from Keycloak's own membership records, not trusted
        // from the form that was just submitted: the submission names an
        // alias, membership in it is still checked here exactly the way
        // authenticate() checked it, so a tampered or stale submission is
        // rejected rather than honoured (issue #80's acceptance criterion).
        List<String> memberships = OrganizationSupport.membershipAliases(context, user);
        String submitted = context.getHttpRequest().getDecodedFormParameters().getFirst(SELECTED_FIELD);

        if (submitted == null || !memberships.contains(submitted)) {
            LOG.infof("organization selector: rejecting a submission naming an organization %s does not belong to",
                    user.getUsername());
            act(context, user, memberships, new OrganizationSelectionDecision.Refused(
                    "the selected organization is not one you belong to"));
            return;
        }
        act(context, user, memberships, new OrganizationSelectionDecision.Selected(submitted));
    }

    private void act(AuthenticationFlowContext context, UserModel user, List<String> memberships,
            OrganizationSelectionDecision.Outcome outcome) {
        if (outcome instanceof OrganizationSelectionDecision.Selected selected) {
            OrganizationSupport.selectOrganization(context, selected.alias());
            LOG.debugf("organization selector: %s selected for %s", selected.alias(), user.getUsername());
            context.success();
            return;
        }

        if (outcome instanceof OrganizationSelectionDecision.NeedsSelection needsSelection) {
            LOG.debugf("organization selector: presenting %d options to %s",
                    needsSelection.options().size(), user.getUsername());
            Response challenge = context.form()
                    .setAttribute("organizations", needsSelection.options())
                    .createForm(SELECTION_FORM);
            // challenge, not success or failure: the flow is neither
            // finished nor refused, it is waiting on the submission
            // action() handles. Keycloak issues no token for a session
            // left here, so abandoning the screen leaves nothing capable
            // of taking a decision (issue #80's acceptance criterion).
            context.challenge(challenge);
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

        // Undecided: an administrator - their own screen, a later slice's
        // concern, is what decides this, not this authenticator.
        // Proceeding unchanged is the deliberate no-op, not an oversight.
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
