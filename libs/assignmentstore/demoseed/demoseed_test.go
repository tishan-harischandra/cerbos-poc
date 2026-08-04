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
	revisions   map[string]assignmentstore.PermissionRevision
	writes      int
}

func newRecordingWriter() *recordingWriter {
	return &recordingWriter{
		permissions: make(map[assignmentstore.RolePermissionKey]assignmentstore.RolePermission),
		revisions:   make(map[string]assignmentstore.PermissionRevision),
	}
}

func (w *recordingWriter) SaveRolePermission(_ context.Context, permission assignmentstore.RolePermission) error {
	w.writes++
	w.permissions[permission.Key] = permission
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
