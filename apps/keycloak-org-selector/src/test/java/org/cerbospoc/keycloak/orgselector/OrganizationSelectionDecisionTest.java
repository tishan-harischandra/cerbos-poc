package org.cerbospoc.keycloak.orgselector;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.util.List;
import java.util.Optional;
import org.cerbospoc.keycloak.orgselector.OrganizationSelectionDecision.Outcome;
import org.cerbospoc.keycloak.orgselector.OrganizationSelectionDecision.Refused;
import org.cerbospoc.keycloak.orgselector.OrganizationSelectionDecision.Selected;
import org.cerbospoc.keycloak.orgselector.OrganizationSelectionDecision.Undecided;
import org.junit.jupiter.api.Test;

/**
 * Every case the PRD's login-flow decision names (issue #79), run as a plain
 * JUnit test with no Keycloak session, request or provider involved.
 */
class OrganizationSelectionDecisionTest {

    @Test
    void anAliasRequestedInScopeMatchingAMembershipIsSelectedWithNoScreen() {
        Outcome outcome = OrganizationSelectionDecision.decide(
                List.of("north-hospital", "south-hospital"), false, Optional.of("south-hospital"));

        Selected selected = assertInstanceOf(Selected.class, outcome);
        assertEquals("south-hospital", selected.alias());
    }

    @Test
    void theRequestedAliasWinsEvenForAnAdministratorWithOtherMemberships() {
        // Bullet 1 in the PRD's ordered list applies before bullet 4's
        // "administrator gets the screen" - an alias the caller already
        // named and belongs to needs no screen, administrator or not.
        Outcome outcome = OrganizationSelectionDecision.decide(
                List.of("north-hospital"), true, Optional.of("north-hospital"));

        Selected selected = assertInstanceOf(Selected.class, outcome);
        assertEquals("north-hospital", selected.alias());
    }

    @Test
    void exactlyOneMembershipAndNotAnAdministratorIsAutoSelectedWithNoScreen() {
        Outcome outcome = OrganizationSelectionDecision.decide(
                List.of("north-hospital"), false, Optional.empty());

        Selected selected = assertInstanceOf(Selected.class, outcome);
        assertEquals("north-hospital", selected.alias());
    }

    @Test
    void moreThanOneMembershipIsUndecidedForTheScreen() {
        Outcome outcome = OrganizationSelectionDecision.decide(
                List.of("north-hospital", "south-hospital"), false, Optional.empty());

        assertInstanceOf(Undecided.class, outcome);
    }

    @Test
    void anAdministratorIsUndecidedForTheScreenEvenWithExactlyOneMembership() {
        // The tenant-wide choice has to remain available to an
        // administrator, so a single membership must not pre-empt it the
        // way it would for an ordinary user.
        Outcome outcome = OrganizationSelectionDecision.decide(
                List.of("north-hospital"), true, Optional.empty());

        assertInstanceOf(Undecided.class, outcome);
    }

    @Test
    void anAdministratorWithNoMembershipIsUndecidedForTheScreenRatherThanRefused() {
        Outcome outcome = OrganizationSelectionDecision.decide(List.of(), true, Optional.empty());

        assertInstanceOf(Undecided.class, outcome);
    }

    @Test
    void noMembershipAndNotAnAdministratorIsRefusedWithAnExplicitReason() {
        Outcome outcome = OrganizationSelectionDecision.decide(List.of(), false, Optional.empty());

        Refused refused = assertInstanceOf(Refused.class, outcome);
        assertEquals(
                "your account is not attached to a hospital; contact your administrator",
                refused.reason());
    }

    @Test
    void aRequestedAliasThatIsNotAMembershipDoesNotOverrideTheOtherRules() {
        // Falls through to the single-membership auto-select rule: the
        // caller asked for an organization they do not belong to, which is
        // not a membership to trust just because it was named.
        Outcome outcome = OrganizationSelectionDecision.decide(
                List.of("north-hospital"), false, Optional.of("south-hospital"));

        Selected selected = assertInstanceOf(Selected.class, outcome);
        assertEquals("north-hospital", selected.alias());
    }

    @Test
    void nullMembershipsIsRejectedRatherThanMisreadAsNoMembership() {
        assertThrows(IllegalArgumentException.class,
                () -> OrganizationSelectionDecision.decide(null, false, Optional.empty()));
    }

    @Test
    void nullRequestedAliasIsRejectedRatherThanMisreadAsNoneRequested() {
        assertThrows(IllegalArgumentException.class,
                () -> OrganizationSelectionDecision.decide(List.of(), false, null));
    }
}
