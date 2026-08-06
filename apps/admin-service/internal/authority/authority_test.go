package authority_test

import (
	"errors"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/authority"
)

func TestAMatchingTenantIsAuthorized(t *testing.T) {
	err := authority.Validate(authority.Principal{TenantID: "tenant-a"}, "tenant-a", "")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestAMismatchedTenantIsRejected(t *testing.T) {
	err := authority.Validate(authority.Principal{TenantID: "tenant-b"}, "tenant-a", "")
	if !errors.Is(err, authority.ErrUnauthorized) {
		t.Fatalf("Validate = %v, want ErrUnauthorized", err)
	}
}

func TestAnEmptyPrincipalTenantIsRejectedEvenAgainstAnEmptyTarget(t *testing.T) {
	// An administrator whose own token carries no tenant has no authority to
	// claim, so this must not pass by both sides being empty.
	err := authority.Validate(authority.Principal{}, "", "")
	if !errors.Is(err, authority.ErrUnauthorized) {
		t.Fatalf("Validate = %v, want ErrUnauthorized", err)
	}
}

func TestAMatchingTenantAndHospitalIsAuthorized(t *testing.T) {
	err := authority.Validate(
		authority.Principal{TenantID: "tenant-a", HospitalID: "hospital-1"},
		"tenant-a", "hospital-1")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestAMismatchedHospitalIsRejectedEvenWithAMatchingTenant(t *testing.T) {
	err := authority.Validate(
		authority.Principal{TenantID: "tenant-a", HospitalID: "hospital-2"},
		"tenant-a", "hospital-1")
	if !errors.Is(err, authority.ErrUnauthorized) {
		t.Fatalf("Validate = %v, want ErrUnauthorized", err)
	}
}

func TestATenantOnlyOperationDoesNotCheckHospital(t *testing.T) {
	// targetHospital empty means the operation is tenant-scoped only (the
	// role-matrix endpoints): the principal's own hospital, whatever it is,
	// must not matter.
	err := authority.Validate(
		authority.Principal{TenantID: "tenant-a", HospitalID: "hospital-9"},
		"tenant-a", "")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
