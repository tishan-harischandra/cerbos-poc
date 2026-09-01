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
 * <p>The PRD names five outcomes, all implemented as of issue #81:
 *
 * <ol>
 *   <li>an alias already requested in scope that matches a membership -
 *       {@link Selected}, no screen;
 *   <li>exactly one membership and not an administrator - {@link Selected},
 *       auto-selected, no screen;
 *   <li>more than one membership and not an administrator -
 *       {@link NeedsSelection}, {@code offerTenantWide=false}, listing
 *       exactly those memberships;
 *   <li>an administrator - {@link NeedsSelection}, {@code
 *       offerTenantWide=true}, unconditionally: even with exactly one
 *       membership, or none, the tenant-wide choice has to be available,
 *       not pre-empted;
 *   <li>no membership and not an administrator - {@link Refused}, an
 *       explicit reason rather than a generic login failure.
 * </ol>
 */
public final class OrganizationSelectionDecision {

    private OrganizationSelectionDecision() {
    }

    /** One of the outcomes {@link #decide} can reach. */
    public sealed interface Outcome permits Selected, SelectedTenantWide, NeedsSelection, Refused {
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
     * The administrator's explicit tenant-wide choice (issue #81): no
     * organization is selected at all, the session carries no active
     * hospital, and - because this is a deliberate act on a screen rather
     * than a fallback nobody chose - it is its own outcome, not
     * {@link Selected} with an empty alias, so the authenticator can log it
     * distinguishably in the audit trail.
     */
    public record SelectedTenantWide() implements Outcome {
    }

    /**
     * A screen is needed. Two shapes share this outcome:
     *
     * <ul>
     *   <li>a non-administrator with more than one membership -
     *       {@code options} lists exactly those memberships,
     *       {@code offerTenantWide} is {@code false};
     *   <li>an administrator, unconditionally - {@code options} lists
     *       their memberships (which may be empty), {@code
     *       offerTenantWide} is {@code true}, since the tenant-wide choice
     *       has to be available whatever their membership count.
     * </ul>
     */
    public record NeedsSelection(List<String> options, boolean offerTenantWide) implements Outcome {
        public NeedsSelection {
            if (options == null) {
                throw new IllegalArgumentException("options must not be null; empty means none");
            }
            if (!offerTenantWide && options.size() < 2) {
                throw new IllegalArgumentException(
                        "a selection with no tenant-wide entry is only offered among two or more options");
            }
            options = List.copyOf(options);
        }
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
            return new NeedsSelection(memberships, true);
        }
        if (memberships.size() == 1) {
            return new Selected(memberships.get(0));
        }
        if (memberships.size() > 1) {
            return new NeedsSelection(memberships, false);
        }
        return new Refused("your account is not attached to a hospital; contact your administrator");
    }
}
