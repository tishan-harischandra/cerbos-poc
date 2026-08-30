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

// The demo installation: one tenant and two canonical roles.
//
// There is no hospital here on purpose. §8.1 scopes role_permission to the
// tenant and gives it no hospital_id column: a role means the same thing across
// a tenant, and it is user overrides that narrow to one hospital. Naming a
// hospital in this seed would imply a dimension the table does not have.
const (
	TenantID = "tenant-a"
	// OtherTenantID holds the grant that tenant isolation must keep out.
	OtherTenantID = "tenant-b"
	// DoctorRole and AuditorRole are canonical Keycloak role identifiers
	// (§7.5), spelled exactly as Appendix B and the policy test suite spell
	// them. §7.5 is explicit that token-to-role normalisation must produce the
	// same identifiers the matrix is keyed by, so a second spelling anywhere
	// would silently resolve to no permissions at all.
	DoctorRole  = "kc:tenant-a:patient-app:doctor"
	AuditorRole = "kc:tenant-a:patient-app:auditor"
	// ResourceKey is the one resource the root policy tree covers so far.
	ResourceKey = "patient_record"
	// HospitalContextResourceKey is the synthetic tenant/hospital scoping
	// resource every X.route.list composite UI capability requires a read
	// on (§12.1). It is not a clinical resource and holds no data of its
	// own; without a grant on it, every list route in the catalog is
	// denied however the rest of the matrix reads.
	HospitalContextResourceKey = "hospital_context"
	// Revision is the demo tenant's permission revision, reported alongside
	// every decision taken against this matrix.
	Revision = 184

	// HospitalID is the hospital every demo principal belongs to, per the
	// realm import's user attributes.
	HospitalID = "hospital-1"
	// OtherHospitalID holds a user override that must never reach a
	// HospitalID decision, even for the very same user (§6.2, §8.3).
	OtherHospitalID = "hospital-2"

	// DoctorWithRevokedUpdate is a real Keycloak user (§7.1's requirement
	// that every seeded identity resolve through the real IdP) whose update
	// grant is revoked by a user override - the case ADR-003 exists for.
	DoctorWithRevokedUpdate = "user-doctor-revoked"
	// ClerkWithGrantedRead holds a role that grants nothing and a user
	// override that grants read on its own.
	ClerkWithGrantedRead = "user-clerk-granted"

	// InstanceScopedResourceID is the one resource instance a user override
	// narrows to below, proving §6.2's optional resource_instance_id
	// selector: the override must apply there and nowhere else.
	InstanceScopedResourceID = "patient-777"

	// The fhir_resource instances issue #9's resource service is a PEP in
	// front of. LockedResourceID is ACTIVE's opposite: the one row that
	// exercises the mandatory locked_record_restriction deny path against
	// real stored state rather than a fixture.
	ActiveResourceID = "patient-456"
	LockedResourceID = "patient-locked-1"

	// GenericResourceType and its instances prove the seed - and so the
	// resource service built against it - is not special-cased to
	// patient_record: any catalog resource type works the same way.
	GenericResourceType        = "condition"
	GenericResourceID          = "condition-1"
	GenericLockedResourceID    = "condition-locked-1"
	GenericOtherTenantResource = "condition-tenant-b-1"
)

// Writer is the part of the store the seed needs. Narrow on purpose: a seeder
// that took the whole port could quietly grow into a second administration
// surface.
type Writer interface {
	SaveRolePermission(ctx context.Context, permission assignmentstore.RolePermission) error
	SaveUserOverride(ctx context.Context, override assignmentstore.UserOverride) error
	SavePermissionRevision(ctx context.Context, revision assignmentstore.PermissionRevision) error
	SaveResource(ctx context.Context, resource assignmentstore.Resource) error
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
		// The two leaves patients.route.list composes, so the Business UI's
		// landing route works on a freshly seeded stack instead of showing
		// the demo clinician /forbidden the moment they log in. The route
		// the walkthrough grants - patient.route.details, which needs
		// person:read - is deliberately still denied here.
		rolePermission(TenantID, DoctorRole, "list", true, began, ends),
		resourceRolePermission(TenantID, DoctorRole, HospitalContextResourceKey,
			"read", true, began, ends),
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

	overrides := []assignmentstore.UserOverride{
		// The ADR-003 case: a user revoke beating a role grant, all the way
		// through the real decision path rather than only inside a policy test.
		userOverride(HospitalID, DoctorWithRevokedUpdate, "update", "",
			assignmentstore.EffectRevoke, true, began, ends),
		// A grant with no role grant behind it: an allow can only have come
		// from the override.
		userOverride(HospitalID, ClerkWithGrantedRead, "read", "",
			assignmentstore.EffectGrant, true, began, ends),
		// Enabled but out of date, so ignoring expiry would visibly grant.
		userOverride(HospitalID, ClerkWithGrantedRead, "update", "",
			assignmentstore.EffectGrant, true, began, expired),
		// The same user's live grant in another hospital, which must never
		// reach a hospital-1 decision.
		userOverride(OtherHospitalID, DoctorWithRevokedUpdate, "delete", "",
			assignmentstore.EffectGrant, true, began, ends),
		// Scoped to one resource instance: it must apply there and nowhere
		// else, which the wide rows above give something to leak into if the
		// scoping were not honoured.
		userOverride(HospitalID, ClerkWithGrantedRead, "delete", InstanceScopedResourceID,
			assignmentstore.EffectGrant, true, began, ends),
	}

	for _, override := range overrides {
		if err := writer.SaveUserOverride(ctx, override); err != nil {
			return fmt.Errorf("seeding an override for %s/%s/%s: %w",
				override.Key.HospitalID, override.Key.UserExternalID, override.Key.ActionKey, err)
		}
	}

	if err := writer.SavePermissionRevision(ctx, assignmentstore.PermissionRevision{
		TenantID:  TenantID,
		Revision:  Revision,
		ChangedAt: at,
	}); err != nil {
		return fmt.Errorf("seeding the permission revision: %w", err)
	}

	resources := []assignmentstore.Resource{
		resource(ResourceKey, ActiveResourceID, TenantID, HospitalID, "ACTIVE", at),
		resource(ResourceKey, InstanceScopedResourceID, TenantID, HospitalID, "ACTIVE", at),
		// The mandatory locked_record_restriction deny path, against a real
		// stored row rather than an attribute the caller supplied itself.
		resource(ResourceKey, LockedResourceID, TenantID, HospitalID, "LOCKED", at),
		// A non-patient_record resource type, proving the resource service
		// built against this seed is generic across the catalog.
		resource(GenericResourceType, GenericResourceID, TenantID, HospitalID, "ACTIVE", at),
		resource(GenericResourceType, GenericLockedResourceID, TenantID, HospitalID, "LOCKED", at),
		// Another tenant's instance, which a tenant-a list or read must
		// never return.
		resource(GenericResourceType, GenericOtherTenantResource, OtherTenantID, HospitalID, "ACTIVE", at),
	}
	for _, r := range resources {
		if err := writer.SaveResource(ctx, r); err != nil {
			return fmt.Errorf("seeding resource %s/%s: %w", r.ResourceType, r.ResourceID, err)
		}
	}

	return nil
}

func resource(resourceType, id, tenant, hospital, status string, at time.Time) assignmentstore.Resource {
	return assignmentstore.Resource{
		ResourceType: resourceType,
		ResourceID:   id,
		TenantID:     tenant,
		HospitalID:   hospital,
		Status:       status,
		PayloadJSON:  fmt.Sprintf(`{"resourceType":%q,"id":%q}`, resourceType, id),
		UpdatedAt:    at,
	}
}

func rolePermission(tenant, role, action string, enabled bool, from, until time.Time) assignmentstore.RolePermission {
	return resourceRolePermission(tenant, role, ResourceKey, action, enabled, from, until)
}

// resourceRolePermission is rolePermission for a resource other than
// patient_record - hospital_context, in particular, which carries the
// scoping grant every list route depends on.
func resourceRolePermission(tenant, role, resource, action string, enabled bool, from, until time.Time) assignmentstore.RolePermission {
	return assignmentstore.RolePermission{
		Key: assignmentstore.RolePermissionKey{
			TenantID:       tenant,
			RoleExternalID: role,
			ResourceKey:    resource,
			ActionKey:      action,
		},
		Enabled:    enabled,
		ValidFrom:  from,
		ValidUntil: until,
		Revision:   Revision,
	}
}

func userOverride(hospital, user, action, instance string, effect assignmentstore.OverrideEffect, enabled bool, from, until time.Time) assignmentstore.UserOverride {
	return assignmentstore.UserOverride{
		Key: assignmentstore.UserOverrideKey{
			TenantID:           TenantID,
			HospitalID:         hospital,
			UserExternalID:     user,
			ResourceKey:        ResourceKey,
			ActionKey:          action,
			ResourceInstanceID: instance,
		},
		Effect:     effect,
		Enabled:    enabled,
		ValidFrom:  from,
		ValidUntil: until,
		Revision:   Revision,
	}
}
