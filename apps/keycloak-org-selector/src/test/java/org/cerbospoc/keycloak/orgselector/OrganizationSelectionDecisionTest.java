package org.cerbospoc.keycloak.orgselector;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.List;
import java.util.Optional;
import org.cerbospoc.keycloak.orgselector.OrganizationSelectionDecision.NeedsSelection;
import org.cerbospoc.keycloak.orgselector.OrganizationSelectionDecision.Outcome;
import org.cerbospoc.keycloak.orgselector.OrganizationSelectionDecision.Refused;
import org.cerbospoc.keycloak.orgselector.OrganizationSelectionDecision.Selected;
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
    void moreThanOneMembershipOffersASelectionAmongExactlyThemWithNoTenantWideEntry() {
        Outcome outcome = OrganizationSelectionDecision.decide(
                List.of("north-hospital", "south-hospital"), false, Optional.empty());

        NeedsSelection needsSelection = assertInstanceOf(NeedsSelection.class, outcome);
        assertEquals(List.of("north-hospital", "south-hospital"), needsSelection.options());
        assertFalse(needsSelection.offerTenantWide(), "an ordinary user's screen offers no tenant-wide entry");
    }

    @Test
    void aRequestedAliasThatDoesNotMatchStillOffersASelectionAmongTheRealMemberships() {
        // The requested alias is not a membership, so it does not win rule
        // 1; falling through to more-than-one-membership is correct, not a
        // side effect of ignoring the request.
        Outcome outcome = OrganizationSelectionDecision.decide(
                List.of("north-hospital", "south-hospital"), false, Optional.of("east-hospital"));

        NeedsSelection needsSelection = assertInstanceOf(NeedsSelection.class, outcome);
        assertEquals(List.of("north-hospital", "south-hospital"), needsSelection.options());
    }

    @Test
    void anAdministratorIsOfferedTheScreenWithATenantWideEntryEvenWithExactlyOneMembership() {
        // The tenant-wide choice has to remain available to an
        // administrator, so a single membership must not pre-empt it the
        // way it would for an ordinary user.
        Outcome outcome = OrganizationSelectionDecision.decide(
                List.of("north-hospital"), true, Optional.empty());

        NeedsSelection needsSelection = assertInstanceOf(NeedsSelection.class, outcome);
        assertEquals(List.of("north-hospital"), needsSelection.options());
        assertTrue(needsSelection.offerTenantWide());
    }

    @Test
    void anAdministratorWithNoMembershipIsOfferedOnlyTheTenantWideEntryRatherThanRefused() {
        Outcome outcome = OrganizationSelectionDecision.decide(List.of(), true, Optional.empty());

        NeedsSelection needsSelection = assertInstanceOf(NeedsSelection.class, outcome);
        assertEquals(List.of(), needsSelection.options());
        assertTrue(needsSelection.offerTenantWide());
    }

    @Test
    void anAdministratorWithMultipleMembershipsIsOfferedThemPlusTheTenantWideEntry() {
        Outcome outcome = OrganizationSelectionDecision.decide(
                List.of("north-hospital", "south-hospital"), true, Optional.empty());

        NeedsSelection needsSelection = assertInstanceOf(NeedsSelection.class, outcome);
        assertEquals(List.of("north-hospital", "south-hospital"), needsSelection.options());
        assertTrue(needsSelection.offerTenantWide());
    }

    @Test
    void aSelectionWithNoTenantWideEntryRequiresAtLeastTwoOptions() {
        // The single-membership and no-membership cases are Selected/Refused,
        // never a one-option screen with nothing else to offer.
        assertThrows(IllegalArgumentException.class,
                () -> new OrganizationSelectionDecision.NeedsSelection(List.of("north-hospital"), false));
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
