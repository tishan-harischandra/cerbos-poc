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
 * The login-flow half of organization selection (issues #79, #80 and #81).
 * Placed after credentials and any second factor, it gathers the facts
 * {@link OrganizationSelectionDecision} needs from the authenticated
 * session and acts on the outcome:
 *
 * <ul>
 *   <li>{@link OrganizationSelectionDecision.Selected} - the organization is
 *       recorded and the flow proceeds with no screen shown;
 *   <li>{@link OrganizationSelectionDecision.NeedsSelection} - the
 *       {@code select-organization.ftl} screen is shown, listing exactly
 *       the offered options and, for an administrator, the tenant-wide
 *       entry; {@link #action} handles its submission;
 *   <li>{@link OrganizationSelectionDecision.SelectedTenantWide} - the
 *       administrator's explicit tenant-wide choice: no organization is
 *       selected, logged as its own distinguishable fact (issue #81's
 *       audit-trail acceptance criterion);
 *   <li>{@link OrganizationSelectionDecision.Refused} - the flow fails with
 *       an explicit reason rather than a generic authentication failure.
 * </ul>
 */
public class OrganizationSelectorAuthenticator implements Authenticator {

    private static final Logger LOG = Logger.getLogger(OrganizationSelectorAuthenticator.class);
    static final String SELECTION_FORM = "select-organization.ftl";
    static final String SELECTED_FIELD = "organization";
    /**
     * The selection screen's own encoding of "tenant-wide" (issue #81): an
     * empty submission is exactly how hospitalId already represents
     * tenant-wide everywhere else in this system (§78's tokenverifier,
     * this authenticator's own {@link OrganizationSupport}), so this is not
     * a second vocabulary for the same fact.
     */
    static final String TENANT_WIDE_VALUE = "";

    @Override
    public void authenticate(AuthenticationFlowContext context) {
        UserModel user = context.getUser();
        List<String> memberships = OrganizationSupport.membershipAliases(context, user);
        boolean isAdministrator = isTenantWideAdministrator(context.getRealm(), user);
        Optional<String> requestedAlias = OrganizationSupport.requestedAlias(context);

        act(context, user, memberships, isAdministrator,
                OrganizationSelectionDecision.decide(memberships, isAdministrator, requestedAlias));
    }

    @Override
    public void action(AuthenticationFlowContext context) {
        UserModel user = context.getUser();
        // Re-derived from Keycloak's own membership and role records, not
        // trusted from the form that was just submitted: the submission
        // names an alias (or tenant-wide), membership - or administrator
        // status, for tenant-wide - is still checked here exactly the way
        // authenticate() checked it, so a tampered or stale submission is
        // rejected rather than honoured (issue #80's acceptance criterion).
        List<String> memberships = OrganizationSupport.membershipAliases(context, user);
        boolean isAdministrator = isTenantWideAdministrator(context.getRealm(), user);
        String submitted = context.getHttpRequest().getDecodedFormParameters().getFirst(SELECTED_FIELD);

        if (TENANT_WIDE_VALUE.equals(submitted)) {
            if (!isAdministrator) {
                LOG.infof("organization selector: rejecting a tenant-wide submission from %s, who is not an administrator",
                        user.getUsername());
                act(context, user, memberships, false, new OrganizationSelectionDecision.Refused(
                        "tenant-wide is only available to an administrator"));
                return;
            }
            act(context, user, memberships, true, new OrganizationSelectionDecision.SelectedTenantWide());
            return;
        }

        if (submitted == null || !memberships.contains(submitted)) {
            LOG.infof("organization selector: rejecting a submission naming an organization %s does not belong to",
                    user.getUsername());
            act(context, user, memberships, isAdministrator, new OrganizationSelectionDecision.Refused(
                    "the selected organization is not one you belong to"));
            return;
        }
        act(context, user, memberships, isAdministrator, new OrganizationSelectionDecision.Selected(submitted));
    }

    private void act(AuthenticationFlowContext context, UserModel user, List<String> memberships,
            boolean isAdministrator, OrganizationSelectionDecision.Outcome outcome) {
        if (outcome instanceof OrganizationSelectionDecision.Selected selected) {
            OrganizationSupport.selectOrganization(context, selected.alias());
            LOG.infof("organization selector: %s chose %s", user.getUsername(), selected.alias());
            context.success();
            return;
        }

        if (outcome instanceof OrganizationSelectionDecision.SelectedTenantWide) {
            // No call to OrganizationSupport.selectOrganization: tenant-wide
            // means precisely that no organization scope is added, so the
            // session carries no active hospital at all (issue #81).
            LOG.infof("organization selector: %s chose tenant-wide", user.getUsername());
            context.success();
            return;
        }

        if (outcome instanceof OrganizationSelectionDecision.NeedsSelection needsSelection) {
            LOG.debugf("organization selector: presenting %s option(s) to %s (tenant-wide offered: %s)",
                    Integer.toString(needsSelection.options().size()), user.getUsername(),
                    needsSelection.offerTenantWide());
            Response challenge = context.form()
                    .setAttribute("organizations", needsSelection.options())
                    .setAttribute("offerTenantWide", needsSelection.offerTenantWide())
                    .setAttribute("tenantWideValue", TENANT_WIDE_VALUE)
                    .createForm(SELECTION_FORM);
            // challenge, not success or failure: the flow is neither
            // finished nor refused, it is waiting on the submission
            // action() handles. Keycloak issues no token for a session
            // left here, so abandoning the screen leaves nothing capable
            // of taking a decision (issue #80's acceptance criterion).
            context.challenge(challenge);
            return;
        }

        // Refused.
        OrganizationSelectionDecision.Refused refused = (OrganizationSelectionDecision.Refused) outcome;
        LOG.infof("organization selector: refusing %s: %s", user.getUsername(), refused.reason());
        Response challenge = context.form()
                .setError(refused.reason())
                .createErrorPage(Response.Status.FORBIDDEN);
        context.failure(AuthenticationFlowError.ACCESS_DENIED, challenge);
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
