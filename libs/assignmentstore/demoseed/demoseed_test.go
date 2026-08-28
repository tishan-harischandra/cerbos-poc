package demoseed_test

import (
	"context"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/demoseed"
)

var seededAt = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

type resourceKey struct {
	resourceType, resourceID string
}

type recordingWriter struct {
	permissions map[assignmentstore.RolePermissionKey]assignmentstore.RolePermission
	overrides   map[assignmentstore.UserOverrideKey]assignmentstore.UserOverride
	revisions   map[string]assignmentstore.PermissionRevision
	resources   map[resourceKey]assignmentstore.Resource
	writes      int
}

func newRecordingWriter() *recordingWriter {
	return &recordingWriter{
		permissions: make(map[assignmentstore.RolePermissionKey]assignmentstore.RolePermission),
		overrides:   make(map[assignmentstore.UserOverrideKey]assignmentstore.UserOverride),
		revisions:   make(map[string]assignmentstore.PermissionRevision),
		resources:   make(map[resourceKey]assignmentstore.Resource),
	}
}

func (w *recordingWriter) SaveResource(_ context.Context, resource assignmentstore.Resource) error {
	w.writes++
	w.resources[resourceKey{resource.ResourceType, resource.ResourceID}] = resource
	return nil
}

func (w *recordingWriter) resource(t *testing.T, resourceType, resourceID string) assignmentstore.Resource {
	t.Helper()
	resource, found := w.resources[resourceKey{resourceType, resourceID}]
	if !found {
		t.Fatalf("the seed wrote no %s/%s resource", resourceType, resourceID)
	}
	return resource
}

func (w *recordingWriter) SaveRolePermission(_ context.Context, permission assignmentstore.RolePermission) error {
	w.writes++
	w.permissions[permission.Key] = permission
	return nil
}

func (w *recordingWriter) SaveUserOverride(_ context.Context, override assignmentstore.UserOverride) error {
	w.writes++
	w.overrides[assignmentstore.NormalizeOverrideKey(override.Key)] = override
	return nil
}

func (w *recordingWriter) SavePermissionRevision(_ context.Context, revision assignmentstore.PermissionRevision) error {
	w.writes++
	w.revisions[revision.TenantID] = revision
	return nil
}

func (w *recordingWriter) permission(t *testing.T, tenant, role, action string) assignmentstore.RolePermission {
	t.Helper()
	return w.permissionFor(t, tenant, role, demoseed.ResourceKey, action)
}

// permissionFor addresses a resource other than patient_record, which the
// seed needs since hospital_context carries the scoping grant every list
// route depends on.
func (w *recordingWriter) permissionFor(t *testing.T, tenant, role, resource, action string) assignmentstore.RolePermission {
	t.Helper()
	key := assignmentstore.RolePermissionKey{
		TenantID: tenant, RoleExternalID: role,
		ResourceKey: resource, ActionKey: action,
	}
	permission, found := w.permissions[key]
	if !found {
		t.Fatalf("the seed wrote no %s/%s/%s/%s row", tenant, role, resource, action)
	}
	return permission
}

func (w *recordingWriter) override(t *testing.T, hospital, user, action, instance string) assignmentstore.UserOverride {
	t.Helper()
	key := assignmentstore.NormalizeOverrideKey(assignmentstore.UserOverrideKey{
		TenantID: demoseed.TenantID, HospitalID: hospital, UserExternalID: user,
		ResourceKey: demoseed.ResourceKey, ActionKey: action, ResourceInstanceID: instance,
	})
	override, found := w.overrides[key]
	if !found {
		t.Fatalf("the seed wrote no %s/%s/%s/%s override", hospital, user, action, instance)
	}
	return override
}

func applySeed(t *testing.T) *recordingWriter {
	t.Helper()
	writer := newRecordingWriter()
	if err := demoseed.Apply(context.Background(), writer, seededAt); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return writer
}

// patients.route.list - the Business UI's landing route - composes
// patient_record:list with hospital_context:read (§12.1). A seed that
// grants neither drops the demo clinician straight onto /forbidden on
// login, which reads as a broken application rather than as the
// deliberate denial it is. patient.route.details stays denied on purpose:
// that is the grant the guided walkthrough makes visible.
func TestTheDoctorRoleCanReachThePatientListRoute(t *testing.T) {
	writer := applySeed(t)

	list := writer.permission(t, demoseed.TenantID, demoseed.DoctorRole, "list")
	if !list.Enabled {
		t.Error("the seeded patient_record:list grant is disabled, so the list route is denied")
	}

	scope := writer.permissionFor(t, demoseed.TenantID, demoseed.DoctorRole,
		demoseed.HospitalContextResourceKey, "read")
	if !scope.Enabled {
		t.Error("the seeded hospital_context:read grant is disabled, so every list route is denied")
	}
}

func TestTheDoctorRoleGrantsReadAndUpdate(t *testing.T) {
	writer := applySeed(t)

	for _, action := range []string{"read", "update"} {
		permission := writer.permission(t, demoseed.TenantID, demoseed.DoctorRole, action)
		if !permission.Enabled {
			t.Errorf("the seeded %s grant is disabled", action)
		}
		if !permission.ValidFrom.Before(seededAt) {
			t.Errorf("the seeded %s grant starts at %s, which is not yet in force at %s",
				action, permission.ValidFrom, seededAt)
		}
		if !permission.ValidUntil.IsZero() && !permission.ValidUntil.After(seededAt) {
			t.Errorf("the seeded %s grant already expired at %s", action, permission.ValidUntil)
		}
	}
}

// The disabled row is the point of the case it supports: it must exist, so that
// "delete is denied" proves a disabled row grants nothing rather than proving
// nobody wrote a row at all.
func TestTheDisabledDeleteRowExistsAndIsDisabled(t *testing.T) {
	permission := applySeed(t).permission(t, demoseed.TenantID, demoseed.DoctorRole, "delete")

	if permission.Enabled {
		t.Error("the delete row is enabled; the disabled case it supports would pass vacuously")
	}
	if !permission.ValidFrom.Before(seededAt) || !permission.ValidUntil.After(seededAt) {
		t.Error("the delete row is outside its validity window, so it would be ignored " +
			"for the wrong reason")
	}
}

// Likewise the expired row: enabled, so that if expiry were ignored it would
// grant, which is what makes the assertion that it does not grant meaningful.
func TestTheExpiredRowIsEnabledButOutOfDate(t *testing.T) {
	permission := applySeed(t).permission(t, demoseed.TenantID, demoseed.AuditorRole, "read")

	if !permission.Enabled {
		t.Error("the expired row is disabled, so the expiry case would pass for the wrong reason")
	}
	if !permission.ValidUntil.Before(seededAt) {
		t.Errorf("the expired row runs until %s, which has not passed at %s",
			permission.ValidUntil, seededAt)
	}
}

// The other tenant's row has to be there for the isolation case to mean
// anything: without it, "another tenant's grant does not leak" is satisfied by
// there being no grant to leak.
func TestTheOtherTenantHasAGrantThatMustNotLeak(t *testing.T) {
	permission := applySeed(t).permission(t, demoseed.OtherTenantID, demoseed.DoctorRole, "delete")

	if !permission.Enabled {
		t.Error("the other tenant's row is disabled, so the isolation case would pass vacuously")
	}
	if permission.Key.TenantID == demoseed.TenantID {
		t.Error("the other tenant's row is in the demo tenant")
	}
}

// The ADR-003 case: a user revoke seeded in the database, not compiled into
// the service, so the end-to-end suite proves it through the real decision
// path rather than only through a policy test.
func TestTheRevokedDoctorHasAUserRevokeOnUpdate(t *testing.T) {
	override := applySeed(t).override(t, demoseed.HospitalID,
		demoseed.DoctorWithRevokedUpdate, "update", assignmentstore.NoResourceInstance)

	if override.Effect != assignmentstore.EffectRevoke {
		t.Errorf("effect = %s, want %s", override.Effect, assignmentstore.EffectRevoke)
	}
	if !override.Enabled {
		t.Error("the revoke row is disabled, so the ADR-003 case would pass vacuously")
	}
}

// A grant with no role grant behind it: an allow can only have come from the
// override.
func TestTheGrantedClerkHasAUserGrantOnRead(t *testing.T) {
	override := applySeed(t).override(t, demoseed.HospitalID,
		demoseed.ClerkWithGrantedRead, "read", assignmentstore.NoResourceInstance)

	if override.Effect != assignmentstore.EffectGrant {
		t.Errorf("effect = %s, want %s", override.Effect, assignmentstore.EffectGrant)
	}
	if !override.Enabled {
		t.Error("the grant row is disabled, so the case would pass vacuously")
	}
}

// Enabled but out of date, the same reasoning as the expired role permission:
// ignoring expiry would visibly grant.
func TestTheClerksUpdateGrantIsEnabledButExpired(t *testing.T) {
	override := applySeed(t).override(t, demoseed.HospitalID,
		demoseed.ClerkWithGrantedRead, "update", assignmentstore.NoResourceInstance)

	if !override.Enabled {
		t.Error("the expired override is disabled, so the expiry case would pass for the wrong reason")
	}
	if !override.ValidUntil.Before(seededAt) {
		t.Errorf("the override runs until %s, which has not passed at %s", override.ValidUntil, seededAt)
	}
}

// The other hospital's row has to exist for the isolation case to mean
// anything, and it has to belong to the very same user: proving isolation by
// tenant or user alone would leave hospital scoping untested.
func TestAnotherHospitalHasAGrantForTheSameUserThatMustNotLeak(t *testing.T) {
	override := applySeed(t).override(t, demoseed.OtherHospitalID,
		demoseed.DoctorWithRevokedUpdate, "delete", assignmentstore.NoResourceInstance)

	if !override.Enabled {
		t.Error("the other hospital's row is disabled, so the isolation case would pass vacuously")
	}
	if override.Key.HospitalID == demoseed.HospitalID {
		t.Error("the other hospital's row is in the demo hospital")
	}
}

// Scoped to one resource instance (§6.2's optional selector): it must apply
// there and, per the seed rows above, nowhere else.
func TestTheInstanceScopedGrantNamesOneResource(t *testing.T) {
	override := applySeed(t).override(t, demoseed.HospitalID,
		demoseed.ClerkWithGrantedRead, "delete", demoseed.InstanceScopedResourceID)

	if !override.Enabled {
		t.Error("the instance-scoped grant is disabled, so the case would pass vacuously")
	}
	if override.Key.ResourceInstanceID != demoseed.InstanceScopedResourceID {
		t.Errorf("resourceInstanceId = %q, want %q",
			override.Key.ResourceInstanceID, demoseed.InstanceScopedResourceID)
	}
}

// The mandatory locked_record_restriction deny path (issue #9) needs a real
// LOCKED row to evaluate against, not an attribute a caller could simply
// choose not to send.
func TestTheLockedResourceIsSeededAsLocked(t *testing.T) {
	resource := applySeed(t).resource(t, demoseed.ResourceKey, demoseed.LockedResourceID)
	if resource.Status != "LOCKED" {
		t.Errorf("status = %q, want LOCKED", resource.Status)
	}
	if resource.TenantID != demoseed.TenantID || resource.HospitalID != demoseed.HospitalID {
		t.Errorf("locked resource tenant/hospital = %s/%s, want %s/%s",
			resource.TenantID, resource.HospitalID, demoseed.TenantID, demoseed.HospitalID)
	}
}

// The active instance is ACTIVE's opposite of the locked one, and must exist
// so "read is still allowed" is not vacuously true for want of a row.
func TestTheActiveResourceIsSeededAsActive(t *testing.T) {
	resource := applySeed(t).resource(t, demoseed.ResourceKey, demoseed.ActiveResourceID)
	if resource.Status != "ACTIVE" {
		t.Errorf("status = %q, want ACTIVE", resource.Status)
	}
}

// A resource type other than patient_record has to be seeded too, or "the
// resource service is generic" would be proven only for the one type it was
// built alongside.
func TestAGenericResourceTypeIsSeededTooAndTheOtherTenantsInstanceIsSeparate(t *testing.T) {
	writer := applySeed(t)

	active := writer.resource(t, demoseed.GenericResourceType, demoseed.GenericResourceID)
	if active.Status != "ACTIVE" {
		t.Errorf("generic resource status = %q, want ACTIVE", active.Status)
	}
	locked := writer.resource(t, demoseed.GenericResourceType, demoseed.GenericLockedResourceID)
	if locked.Status != "LOCKED" {
		t.Errorf("generic locked resource status = %q, want LOCKED", locked.Status)
	}

	otherTenant := writer.resource(t, demoseed.GenericResourceType, demoseed.GenericOtherTenantResource)
	if otherTenant.TenantID != demoseed.OtherTenantID {
		t.Errorf("the other-tenant resource's tenant = %q, want %q",
			otherTenant.TenantID, demoseed.OtherTenantID)
	}
}

func TestTheDemoTenantCarriesTheDemoRevision(t *testing.T) {
	revision, found := applySeed(t).revisions[demoseed.TenantID]
	if !found {
		t.Fatalf("the seed wrote no revision for %s", demoseed.TenantID)
	}
	if revision.Revision != demoseed.Revision {
		t.Errorf("revision = %d, want %d", revision.Revision, demoseed.Revision)
	}
}

// The seed runs on every `make up`, including against a database that already
// has it. A second run must leave the same rows rather than failing or
// duplicating.
func TestSeedingTwiceLeavesTheSameRows(t *testing.T) {
	writer := newRecordingWriter()
	ctx := context.Background()

	if err := demoseed.Apply(ctx, writer, seededAt); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	afterFirst := len(writer.permissions)
	resourcesAfterFirst := len(writer.resources)
	writesPerRun := writer.writes

	if err := demoseed.Apply(ctx, writer, seededAt); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	if len(writer.permissions) != afterFirst {
		t.Errorf("a second seeding left %d rows, want %d", len(writer.permissions), afterFirst)
	}
	if len(writer.resources) != resourcesAfterFirst {
		t.Errorf("a second seeding left %d resources, want %d", len(writer.resources), resourcesAfterFirst)
	}
	if writer.writes != 2*writesPerRun {
		t.Errorf("the second run made %d writes, want %d", writer.writes-writesPerRun, writesPerRun)
	}
}
