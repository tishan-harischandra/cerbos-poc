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
 * <p>The PRD names five outcomes. Issue #79 implemented the three that need
 * no screen; issue #80 adds the screen itself for the ordinary
 * multi-membership case ({@link NeedsSelection}). An administrator's own
 * screen - which the PRD gives an additional tenant-wide entry, not just a
 * list of memberships - remains {@link Undecided}: a distinct concept from
 * "pick one of these memberships", and not part of either slice's scope.
 *
 * <ol>
 *   <li>an alias already requested in scope that matches a membership -
 *       {@link Selected}, no screen;
 *   <li>exactly one membership and not an administrator - {@link Selected},
 *       auto-selected, no screen;
 *   <li>more than one membership and not an administrator -
 *       {@link NeedsSelection}, listing exactly those memberships;
 *   <li>an administrator - {@link Undecided}, even with exactly one
 *       membership: the tenant-wide choice has to be available, not
 *       pre-empted, and this slice's screen has no tenant-wide entry to
 *       offer it with;
 *   <li>no membership and not an administrator - {@link Refused}, an
 *       explicit reason rather than a generic login failure.
 * </ol>
 */
public final class OrganizationSelectionDecision {

    private OrganizationSelectionDecision() {
    }

    /** One of the outcomes {@link #decide} can reach. */
    public sealed interface Outcome permits Selected, NeedsSelection, Undecided, Refused {
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
     * The user belongs to more than one organization: the screen this
     * outcome exists for (issue #80) lists exactly {@code options}, in the
     * order given - never more, never a hospital they do not belong to.
     */
    public record NeedsSelection(List<String> options) implements Outcome {
        public NeedsSelection {
            if (options == null || options.size() < 2) {
                throw new IllegalArgumentException("a selection is only offered among two or more options");
            }
            options = List.copyOf(options);
        }
    }

    /**
     * An administrator: their own screen - a tenant-wide entry alongside
     * their memberships - is neither of this slice's outcomes, so this
     * authenticator lets the flow continue unchanged rather than forcing
     * one it has no basis for.
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
        if (isAdministrator) {
            return new Undecided();
        }
        if (memberships.size() == 1) {
            return new Selected(memberships.get(0));
        }
        if (memberships.size() > 1) {
            return new NeedsSelection(memberships);
        }
        return new Refused("your account is not attached to a hospital; contact your administrator");
    }
}
