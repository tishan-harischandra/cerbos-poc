package org.cerbospoc.keycloak.orgselector;

import java.util.List;
import java.util.Optional;

/**
 * The organization-selection decision, as a pure function of facts an
 * authenticator can gather from a session: the user's own organization
 * memberships, whether they hold the tenant-wide realm role, and any
 * organization alias already named in the requested scope (issue #79).
 *
 * <p>This class touches no Keycloak session, request or provider - every
 * case is a JUnit assertion, not a running server - so the flow logic in
 * {@link OrganizationSelectorAuthenticator} has nothing left to decide, only
 * to gather inputs and act on the outcome.
 *
 * <p>The PRD names five outcomes; this slice's authenticator acts on three
 * of them (the ones that need no selection screen) and leaves
 * {@link Undecided} for the ones the screen - a later slice - will resolve:
 *
 * <ol>
 *   <li>an alias already requested in scope that matches a membership -
 *       {@link Selected}, no screen;
 *   <li>exactly one membership and not an administrator - {@link Selected},
 *       auto-selected, no screen;
 *   <li>more than one membership - {@link Undecided} (the screen);
 *   <li>an administrator - {@link Undecided} (the screen, with a tenant-wide
 *       entry), even with exactly one membership: the tenant-wide choice has
 *       to be available, not pre-empted;
 *   <li>no membership and not an administrator - {@link Refused}, an
 *       explicit reason rather than a generic login failure.
 * </ol>
 */
public final class OrganizationSelectionDecision {

    private OrganizationSelectionDecision() {
    }

    /** One of the outcomes {@link #decide} can reach. */
    public sealed interface Outcome permits Selected, Undecided, Refused {
    }

    /** The organization alias to select, silently, with no screen shown. */
    public record Selected(String alias) implements Outcome {
        public Selected {
            if (alias == null || alias.isBlank()) {
                throw new IllegalArgumentException("a selected alias must not be blank");
            }
        }
    }

    /**
     * No case this slice handles applies; the selection screen - not
     * implemented by this authenticator - decides. The authenticator lets
     * the flow continue unchanged rather than forcing an outcome it has no
     * basis for.
     */
    public record Undecided() implements Outcome {
    }

    /** Login is refused outright, with a reason to show the user. */
    public record Refused(String reason) implements Outcome {
        public Refused {
            if (reason == null || reason.isBlank()) {
                throw new IllegalArgumentException("a refusal must carry a reason");
            }
        }
    }

    /**
     * decide is the pure function: every input is a fact already gathered
     * from a verified source (Keycloak's own organization membership
     * records, the user's own realm roles, the request's own scope
     * parameter), never from anything a caller wrote into a form field this
     * decision reads back.
     *
     * @param memberships    the aliases of every organization the user
     *                       belongs to, in no particular order
     * @param isAdministrator whether the user holds the tenant-wide realm
     *                       role (issue #78's {@code admin})
     * @param requestedAlias the organization alias named in the request's
     *                       own {@code scope} parameter, if any
     */
    public static Outcome decide(List<String> memberships, boolean isAdministrator, Optional<String> requestedAlias) {
        if (memberships == null) {
            throw new IllegalArgumentException("memberships must not be null; empty means none");
        }
        if (requestedAlias == null) {
            throw new IllegalArgumentException("requestedAlias must not be null; Optional.empty() means none requested");
        }

        if (requestedAlias.isPresent() && memberships.contains(requestedAlias.get())) {
            return new Selected(requestedAlias.get());
        }
        if (!isAdministrator && memberships.size() == 1) {
            return new Selected(memberships.get(0));
        }
        if (isAdministrator || memberships.size() > 1) {
            return new Undecided();
        }
        return new Refused("your account is not attached to a hospital; contact your administrator");
    }
}
