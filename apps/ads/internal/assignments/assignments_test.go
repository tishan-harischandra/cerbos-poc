package assignments_test

import (
	"context"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/assignments"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

func TestAKnownPrincipalResolvesToItsSeededAssignments(t *testing.T) {
	store := assignments.NewFixtures()

	input, err := store.For(context.Background(), authz.AssignmentQuery{
		TenantID:     "tenant-a",
		HospitalID:   "hospital-1",
		PrincipalID:  assignments.DoctorWithRevokedUpdate,
		ResourceKind: "patient_record",
		ResourceID:   "patient-456",
	})
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	assembled := permissioncontext.Assemble(input)
	if !contains(assembled.RoleGrantedActions, "read") {
		t.Errorf("roleGrantedActions = %v, want it to contain read", assembled.RoleGrantedActions)
	}
	if !contains(assembled.UserRevokedActions, "update") {
		t.Errorf("userRevokedActions = %v, want it to contain update", assembled.UserRevokedActions)
	}
}

// An unknown principal must resolve to no permissions at all rather than to an
// error or a default grant. Cerbos then reaches default deny on its own.
func TestAnUnknownPrincipalResolvesToNoPermissions(t *testing.T) {
	store := assignments.NewFixtures()

	input, err := store.For(context.Background(), authz.AssignmentQuery{
		TenantID:     "tenant-a",
		HospitalID:   "hospital-1",
		PrincipalID:  "nobody-we-know",
		ResourceKind: "patient_record",
		ResourceID:   "patient-456",
	})
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	assembled := permissioncontext.Assemble(input)
	if len(assembled.RoleGrantedActions) != 0 ||
		len(assembled.UserGrantedActions) != 0 ||
		len(assembled.UserRevokedActions) != 0 {
		t.Errorf("an unknown principal resolved to %+v, want empty action sets", assembled)
	}
}

// Assignments are scoped per tenant. The same principal ID in another tenant
// must not inherit the seeded permissions.
func TestAssignmentsDoNotLeakAcrossTenants(t *testing.T) {
	store := assignments.NewFixtures()

	input, err := store.For(context.Background(), authz.AssignmentQuery{
		TenantID:     "tenant-b",
		HospitalID:   "hospital-1",
		PrincipalID:  assignments.DoctorWithRoleGrants,
		ResourceKind: "patient_record",
		ResourceID:   "patient-456",
	})
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	assembled := permissioncontext.Assemble(input)
	if len(assembled.RoleGrantedActions) != 0 {
		t.Errorf("tenant-b resolved role grants %v, want none", assembled.RoleGrantedActions)
	}
}

func TestAssignmentsAreScopedToTheResourceKind(t *testing.T) {
	store := assignments.NewFixtures()

	input, err := store.For(context.Background(), authz.AssignmentQuery{
		TenantID:     "tenant-a",
		HospitalID:   "hospital-1",
		PrincipalID:  assignments.DoctorWithRoleGrants,
		ResourceKind: "prescription",
		ResourceID:   "rx-1",
	})
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	assembled := permissioncontext.Assemble(input)
	if len(assembled.RoleGrantedActions) != 0 {
		t.Errorf("an unseeded resource kind resolved grants %v, want none", assembled.RoleGrantedActions)
	}
}

func TestEveryPrecedenceFixtureIsReachable(t *testing.T) {
	store := assignments.NewFixtures()

	for _, principal := range []string{
		assignments.DoctorWithRoleGrants,
		assignments.DoctorWithRevokedUpdate,
		assignments.ClerkWithUserGrantOnly,
		assignments.PrincipalWithNoAssignments,
	} {
		t.Run(principal, func(t *testing.T) {
			if _, err := store.For(context.Background(), authz.AssignmentQuery{
				TenantID:     "tenant-a",
				HospitalID:   "hospital-1",
				PrincipalID:  principal,
				ResourceKind: "patient_record",
				ResourceID:   "patient-456",
			}); err != nil {
				t.Fatalf("For(%s): %v", principal, err)
			}
		})
	}
}

func contains(actions []string, want string) bool {
	for _, action := range actions {
		if action == want {
			return true
		}
	}
	return false
}
