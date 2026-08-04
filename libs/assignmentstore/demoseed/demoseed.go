// Package demoseed writes the smallest role matrix that exercises the runtime
// decision path end to end.
//
// It is deliberately not a fixture file: the rows go through the same
// assignmentstore port every other writer uses, so the seed proves the write
// path as well as populating the read path, and it works on either engine
// without knowing which one it reached.
//
// Every row here exists to make one end-to-end assertion mean something. A
// disabled row and an expired row are both enabled-looking in every respect
// except the one being tested, and the other tenant genuinely holds the grant
// that must not leak - otherwise "it did not leak" would be satisfied by there
// being nothing to leak.
package demoseed

import (
	"context"
	"fmt"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// The demo installation: one tenant, one hospital, two canonical roles.
const (
	TenantID = "tenant-a"
	// OtherTenantID holds the grant that tenant isolation must keep out.
	OtherTenantID = "tenant-b"
	HospitalID    = "hospital-1"
	// DoctorRole and AuditorRole are canonical Keycloak role identifiers
	// (§7.5), spelled exactly as Appendix B and the policy test suite spell
	// them. §7.5 is explicit that token-to-role normalisation must produce the
	// same identifiers the matrix is keyed by, so a second spelling anywhere
	// would silently resolve to no permissions at all.
	DoctorRole  = "kc:realm:patient-app:doctor"
	AuditorRole = "kc:realm:patient-app:auditor"
	// ResourceKey is the one resource the root policy tree covers so far.
	ResourceKey = "patient_record"
	// Revision is the demo tenant's permission revision, reported alongside
	// every decision taken against this matrix.
	Revision = 184
)

// Writer is the part of the store the seed needs. Narrow on purpose: a seeder
// that took the whole port could quietly grow into a second administration
// surface.
type Writer interface {
	SaveRolePermission(ctx context.Context, permission assignmentstore.RolePermission) error
	SavePermissionRevision(ctx context.Context, revision assignmentstore.PermissionRevision) error
}

// Apply writes the demo matrix. It is idempotent: every row is saved on its
// §8.2 unique key, so running it against an already-seeded database leaves the
// same rows rather than failing or duplicating.
//
// at is the instant the validity windows are laid out around, so a seed and the
// decisions taken against it agree on what "already expired" means.
func Apply(ctx context.Context, writer Writer, at time.Time) error {
	began := at.AddDate(-1, 0, 0)
	ends := at.AddDate(1, 0, 0)
	expired := at.AddDate(0, -1, 0)

	permissions := []assignmentstore.RolePermission{
		// The ordinary case: a role grant that allows.
		rolePermission(TenantID, DoctorRole, "read", true, began, ends),
		rolePermission(TenantID, DoctorRole, "update", true, began, ends),
		// Disabled: in force, but granting nothing. Not a denial (§8.3).
		rolePermission(TenantID, DoctorRole, "delete", false, began, ends),
		// Enabled but out of date, so ignoring expiry would visibly grant.
		rolePermission(TenantID, AuditorRole, "read", true, began, expired),
		// Another tenant's live grant, which must never reach a tenant-a
		// decision.
		rolePermission(OtherTenantID, DoctorRole, "delete", true, began, ends),
	}

	for _, permission := range permissions {
		if err := writer.SaveRolePermission(ctx, permission); err != nil {
			return fmt.Errorf("seeding %s/%s/%s: %w",
				permission.Key.TenantID, permission.Key.RoleExternalID, permission.Key.ActionKey, err)
		}
	}

	if err := writer.SavePermissionRevision(ctx, assignmentstore.PermissionRevision{
		TenantID:  TenantID,
		Revision:  Revision,
		ChangedAt: at,
	}); err != nil {
		return fmt.Errorf("seeding the permission revision: %w", err)
	}

	return nil
}

func rolePermission(tenant, role, action string, enabled bool, from, until time.Time) assignmentstore.RolePermission {
	return assignmentstore.RolePermission{
		Key: assignmentstore.RolePermissionKey{
			TenantID:       tenant,
			RoleExternalID: role,
			ResourceKey:    ResourceKey,
			ActionKey:      action,
		},
		Enabled:    enabled,
		ValidFrom:  from,
		ValidUntil: until,
		Revision:   Revision,
	}
}
