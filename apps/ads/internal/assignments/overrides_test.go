package assignments_test

import (
	"context"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/assignments"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

func overrideQuery(principal, tenant, kind string) authz.AssignmentQuery {
	return authz.AssignmentQuery{
		TenantID:     tenant,
		HospitalID:   "hospital-1",
		PrincipalID:  principal,
		ResourceKind: kind,
		ResourceID:   "patient-456",
	}
}

func TestASeededPrincipalResolvesToItsOverride(t *testing.T) {
	overrides, err := assignments.NewSeededOverrides().For(context.Background(),
		overrideQuery(assignments.DoctorWithRevokedUpdate, "tenant-a", "patient_record"))
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	assembled := permissioncontext.Assemble(permissioncontext.Input{UserOverrides: overrides})
	if !contains(assembled.UserRevokedActions, "update") {
		t.Errorf("userRevokedActions = %v, want it to contain update", assembled.UserRevokedActions)
	}
}

// An unknown principal has no overrides, which is INHERIT: the role result
// stands. It is not an error and not a denial.
func TestAnUnknownPrincipalHasNoOverrides(t *testing.T) {
	overrides, err := assignments.NewSeededOverrides().For(context.Background(),
		overrideQuery("nobody-we-know", "tenant-a", "patient_record"))
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if len(overrides) != 0 {
		t.Errorf("an unknown principal resolved %v, want no overrides", overrides)
	}
}

func TestOverridesDoNotLeakAcrossTenants(t *testing.T) {
	overrides, err := assignments.NewSeededOverrides().For(context.Background(),
		overrideQuery(assignments.DoctorWithRevokedUpdate, "tenant-b", "patient_record"))
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if len(overrides) != 0 {
		t.Errorf("tenant-b resolved %v, want no overrides", overrides)
	}
}

func TestOverridesAreScopedToTheResourceKind(t *testing.T) {
	overrides, err := assignments.NewSeededOverrides().For(context.Background(),
		overrideQuery(assignments.DoctorWithRevokedUpdate, "tenant-a", "prescription"))
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if len(overrides) != 0 {
		t.Errorf("an unseeded resource kind resolved %v, want no overrides", overrides)
	}
}

func TestEveryPrecedenceFixtureIsReachable(t *testing.T) {
	seeded := assignments.NewSeededOverrides()

	for _, principal := range assignments.Principals() {
		t.Run(principal, func(t *testing.T) {
			if _, err := seeded.For(context.Background(),
				overrideQuery(principal, "tenant-a", "patient_record")); err != nil {
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
