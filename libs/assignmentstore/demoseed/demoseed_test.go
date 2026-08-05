package demoseed_test

import (
	"context"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/demoseed"
)

var seededAt = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

type recordingWriter struct {
	permissions map[assignmentstore.RolePermissionKey]assignmentstore.RolePermission
	overrides   map[assignmentstore.UserOverrideKey]assignmentstore.UserOverride
	revisions   map[string]assignmentstore.PermissionRevision
	writes      int
}

func newRecordingWriter() *recordingWriter {
	return &recordingWriter{
		permissions: make(map[assignmentstore.RolePermissionKey]assignmentstore.RolePermission),
		overrides:   make(map[assignmentstore.UserOverrideKey]assignmentstore.UserOverride),
		revisions:   make(map[string]assignmentstore.PermissionRevision),
	}
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
	key := assignmentstore.RolePermissionKey{
		TenantID: tenant, RoleExternalID: role,
		ResourceKey: demoseed.ResourceKey, ActionKey: action,
	}
	permission, found := w.permissions[key]
	if !found {
		t.Fatalf("the seed wrote no %s/%s/%s row", tenant, role, action)
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
	writesPerRun := writer.writes

	if err := demoseed.Apply(ctx, writer, seededAt); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	if len(writer.permissions) != afterFirst {
		t.Errorf("a second seeding left %d rows, want %d", len(writer.permissions), afterFirst)
	}
	if writer.writes != 2*writesPerRun {
		t.Errorf("the second run made %d writes, want %d", writer.writes-writesPerRun, writesPerRun)
	}
}
