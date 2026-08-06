// Package storecontract is the one suite every assignmentstore adapter must
// pass, unchanged, on every engine.
//
// The assertions here never mention an engine. Portability is a property of the
// schema and the adapters, so a test that had to ask which database it was
// talking to would be conceding the point the suite exists to prove.
package storecontract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// Tables are the §8.1 tables the migrations must create on every engine.
var Tables = []string{
	"authorization_action",
	"authorization_resource",
	"fhir_resource",
	"installation_config",
	"outbox_event",
	"permission_audit_event",
	"permission_revision",
	"role_permission",
	"ui_capability_definition",
	"user_permission_override",
}

// Factory opens a store for one engine. It is called once per subtest so a
// failure in one case cannot leak connection state into the next.
type Factory func(t *testing.T) assignmentstore.Store

// Run executes the whole contract against one engine.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("the migrated schema carries every authorization table", func(t *testing.T) {
		assertEveryTableExists(t, newStore(t))
	})
	t.Run("the schema declares the unique role permission key", func(t *testing.T) {
		assertUniqueKeyColumns(t, newStore(t), "role_permission",
			[]string{"tenant_id", "role_external_id", "resource_key", "action_key"})
	})
	t.Run("the schema declares the unique user override key", func(t *testing.T) {
		assertUniqueKeyColumns(t, newStore(t), "user_permission_override",
			[]string{"tenant_id", "hospital_id", "user_external_id",
				"resource_key", "action_key", "resource_instance_id"})
	})
	t.Run("the unique role permission key is enforced", func(t *testing.T) {
		assertRolePermissionKeyEnforced(t, newStore(t))
	})
	t.Run("the unique user override key is enforced", func(t *testing.T) {
		assertUserOverrideKeyEnforced(t, newStore(t))
	})
	t.Run("role permissions are indexed by tenant and role", func(t *testing.T) {
		assertRolePermissionIndex(t, newStore(t))
	})
	t.Run("user overrides are indexed for active lookups", func(t *testing.T) {
		assertUserOverrideIndex(t, newStore(t))
	})
	t.Run("a disabled role permission round-trips as disabled", func(t *testing.T) {
		assertBooleanRoundTrip(t, newStore(t))
	})
	t.Run("an instant keeps its meaning across time zones", func(t *testing.T) {
		assertTimestampRoundTrip(t, newStore(t))
	})
	t.Run("json columns round-trip byte for byte", func(t *testing.T) {
		assertJSONRoundTrip(t, newStore(t))
	})
	t.Run("an override with no named instance applies to every instance", func(t *testing.T) {
		assertEmptyResourceInstance(t, newStore(t))
	})
	t.Run("an unpublished outbox event has no publication time", func(t *testing.T) {
		assertNullableTimestamp(t, newStore(t))
	})
	t.Run("an absent optional text reads back the same on either engine", func(t *testing.T) {
		assertAbsentOptionalText(t, newStore(t))
	})
	t.Run("a tenant's permission revision is readable and advances in place", func(t *testing.T) {
		assertPermissionRevision(t, newStore(t))
	})
	t.Run("the active role matrix for a set of roles is read in one call", func(t *testing.T) {
		assertActiveRolePermissions(t, newStore(t))
	})
	t.Run("a role permission with no end date stays active", func(t *testing.T) {
		assertOpenEndedRolePermission(t, newStore(t))
	})
	t.Run("RolePermissionsForRole reads every row for a role, unfiltered by validity or enabled state", func(t *testing.T) {
		assertRolePermissionsForRole(t, newStore(t))
	})
	t.Run("the active user overrides for one principal and resource are read in one call", func(t *testing.T) {
		assertActiveUserOverrides(t, newStore(t))
	})
	t.Run("deleting a resource removes it and is idempotent", func(t *testing.T) {
		assertDeleteResource(t, newStore(t))
	})
	t.Run("listing resources pages within one tenant and hospital", func(t *testing.T) {
		assertListResources(t, newStore(t))
	})
	t.Run("SaveRoleMatrix commits the permission, audit, outbox and revision together", func(t *testing.T) {
		assertSaveRoleMatrixAtomicSuccess(t, newStore(t))
	})
	t.Run("SaveRoleMatrix rejects a stale expected revision and writes nothing", func(t *testing.T) {
		assertSaveRoleMatrixStaleRevision(t, newStore(t))
	})
	t.Run("two concurrent SaveRoleMatrix calls against the same role yield one success and one conflict", func(t *testing.T) {
		assertSaveRoleMatrixConcurrentWrites(t, newStore(t))
	})
	t.Run("a mid-transaction failure in SaveRoleMatrix rolls back all four writes", func(t *testing.T) {
		assertSaveRoleMatrixRollsBackOnFailure(t, newStore(t))
	})
	t.Run("audit history survives SaveRoleMatrix updating the same role permission in place", func(t *testing.T) {
		assertSaveRoleMatrixAuditHistoryIsAppendOnly(t, newStore(t))
	})
	t.Run("unpublished outbox events are listed oldest first and can be marked published", func(t *testing.T) {
		assertUnpublishedOutboxEvents(t, newStore(t))
	})
	t.Run("SaveUserOverrideWrite commits a GRANT or REVOKE with its audit, outbox and revision together", func(t *testing.T) {
		assertSaveUserOverrideWriteAtomicSuccess(t, newStore(t))
	})
	t.Run("SaveUserOverrideWrite rejects a stale expected revision and writes nothing", func(t *testing.T) {
		assertSaveUserOverrideWriteStaleRevision(t, newStore(t))
	})
	t.Run("SaveUserOverrideWrite with no effect clears an existing override row", func(t *testing.T) {
		assertSaveUserOverrideWriteInheritClearsTheRow(t, newStore(t))
	})
}

// The hot path resolves every role a principal holds at once (§11.2). What that
// lookup must and must not return is the whole of this case.
//
// Note what is *not* filtered here: a disabled row comes back marked disabled
// rather than being dropped. Disabled means "contributes no grant", not "denies"
// (§8.3), and turning it into an absence here would leave the caller unable to
// tell a row that exists and grants nothing from a row that was never written.
// Validity is filtered, because §8.3 says an expired assignment is ignored
// outright and the validity columns are indexed for exactly this predicate.
func assertActiveRolePermissions(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "role_permission")

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	lastYear := now.AddDate(-1, 0, 0)
	nextYear := now.AddDate(1, 0, 0)

	save := func(tenant, role, resource, action string, enabled bool, from, until time.Time) {
		t.Helper()
		if err := store.SaveRolePermission(ctx, assignmentstore.RolePermission{
			Key: assignmentstore.RolePermissionKey{
				TenantID: tenant, RoleExternalID: role,
				ResourceKey: resource, ActionKey: action,
			},
			Enabled: enabled, ValidFrom: from, ValidUntil: until, Revision: 12,
		}); err != nil {
			t.Fatalf("saving %s/%s/%s/%s: %v", tenant, role, resource, action, err)
		}
	}

	save("tenant-a", "role-doctor", "patient_record", "read", true, lastYear, nextYear)
	save("tenant-a", "role-nurse", "patient_record", "list", true, lastYear, nextYear)
	// Disabled: present, but granting nothing.
	save("tenant-a", "role-doctor", "patient_record", "delete", false, lastYear, nextYear)
	// Expired and not yet in force: both invisible to a decision taken now.
	save("tenant-a", "role-doctor", "patient_record", "update", true, lastYear, now.AddDate(0, -1, 0))
	save("tenant-a", "role-doctor", "patient_record", "create", true, now.AddDate(0, 1, 0), nextYear)
	// Another tenant, another resource and a role nobody asked about.
	save("tenant-b", "role-doctor", "patient_record", "read", true, lastYear, nextYear)
	save("tenant-a", "role-doctor", "prescription", "read", true, lastYear, nextYear)
	save("tenant-a", "role-porter", "patient_record", "read", true, lastYear, nextYear)

	found, err := store.ActiveRolePermissions(ctx, assignmentstore.ActiveRolePermissionQuery{
		TenantID:        "tenant-a",
		RoleExternalIDs: []string{"role-doctor", "role-nurse"},
		ResourceKey:     "patient_record",
		At:              now,
	})
	if err != nil {
		t.Fatalf("reading the active role matrix: %v", err)
	}

	got := make(map[string]bool, len(found))
	for _, permission := range found {
		if permission.Key.TenantID != "tenant-a" {
			t.Errorf("a %s row reached a tenant-a lookup", permission.Key.TenantID)
		}
		if permission.Key.ResourceKey != "patient_record" {
			t.Errorf("a %s row reached a patient_record lookup", permission.Key.ResourceKey)
		}
		got[permission.Key.RoleExternalID+"/"+permission.Key.ActionKey] = permission.Enabled
	}

	want := map[string]bool{
		"role-doctor/read":   true,
		"role-doctor/delete": false,
		"role-nurse/list":    true,
	}
	if len(got) != len(want) {
		t.Fatalf("the active matrix is %v, want %v", got, want)
	}
	for key, enabled := range want {
		stored, present := got[key]
		if !present {
			t.Errorf("%s is missing from the active matrix %v", key, got)
			continue
		}
		if stored != enabled {
			t.Errorf("%s came back enabled=%t, want %t", key, stored, enabled)
		}
	}

	// An empty role set is a principal with no roles, not a request for
	// everything: it must read as no permissions rather than as the tenant's
	// whole matrix.
	none, err := store.ActiveRolePermissions(ctx, assignmentstore.ActiveRolePermissionQuery{
		TenantID:    "tenant-a",
		ResourceKey: "patient_record",
		At:          now,
	})
	if err != nil {
		t.Fatalf("reading with no roles: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("a principal with no roles resolved %d permissions, want none", len(none))
	}
}

// valid_until is nullable: a grant with no planned end is the ordinary case, and
// it must not be mistaken for one that expired at the zero instant.
func assertOpenEndedRolePermission(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "role_permission")

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	key := assignmentstore.RolePermissionKey{
		TenantID: "tenant-a", RoleExternalID: "role-doctor",
		ResourceKey: "patient_record", ActionKey: "read",
	}
	if err := store.SaveRolePermission(ctx, assignmentstore.RolePermission{
		Key: key, Enabled: true, ValidFrom: now.AddDate(-1, 0, 0), Revision: 3,
	}); err != nil {
		t.Fatalf("saving an open-ended permission: %v", err)
	}

	stored, found, err := store.RolePermission(ctx, key)
	if err != nil || !found {
		t.Fatalf("reading it back: found=%t err=%v", found, err)
	}
	if !stored.ValidUntil.IsZero() {
		t.Errorf("validUntil = %s, want the zero instant for an open-ended grant", stored.ValidUntil)
	}

	active, err := store.ActiveRolePermissions(ctx, assignmentstore.ActiveRolePermissionQuery{
		TenantID:        "tenant-a",
		RoleExternalIDs: []string{"role-doctor"},
		ResourceKey:     "patient_record",
		At:              now,
	})
	if err != nil {
		t.Fatalf("reading the active matrix: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("an open-ended grant resolved %d permissions, want 1", len(active))
	}
}

// RolePermissionsForRole is the role matrix screen's read: everything a
// role carries, exactly as stored, including a disabled row and one whose
// validity window has already closed - neither of which ActiveRolePermissions
// would return.
func assertRolePermissionsForRole(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "role_permission")

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	save := func(resource, action string, enabled bool, from, until time.Time) {
		t.Helper()
		if err := store.SaveRolePermission(ctx, assignmentstore.RolePermission{
			Key: assignmentstore.RolePermissionKey{
				TenantID: "tenant-a", RoleExternalID: "role-doctor",
				ResourceKey: resource, ActionKey: action,
			},
			Enabled: enabled, ValidFrom: from, ValidUntil: until, Revision: 1,
		}); err != nil {
			t.Fatalf("saving %s/%s: %v", resource, action, err)
		}
	}

	save("patient_record", "read", true, now.AddDate(-1, 0, 0), time.Time{})
	save("patient_record", "delete", false, now.AddDate(-1, 0, 0), time.Time{})
	save("medication_request", "read", true, now.AddDate(-2, 0, 0), now.AddDate(-1, 0, 0))
	// A different role's row must never appear.
	if err := store.SaveRolePermission(ctx, assignmentstore.RolePermission{
		Key: assignmentstore.RolePermissionKey{
			TenantID: "tenant-a", RoleExternalID: "role-nurse",
			ResourceKey: "patient_record", ActionKey: "read",
		},
		Enabled: true, ValidFrom: now.AddDate(-1, 0, 0), Revision: 1,
	}); err != nil {
		t.Fatalf("saving role-nurse's row: %v", err)
	}

	found, err := store.RolePermissionsForRole(ctx, "tenant-a", "role-doctor")
	if err != nil {
		t.Fatalf("RolePermissionsForRole: %v", err)
	}
	if len(found) != 3 {
		t.Fatalf("got %d permissions, want 3 (all of role-doctor's rows, none of role-nurse's)", len(found))
	}

	byAction := make(map[string]assignmentstore.RolePermission, len(found))
	for _, permission := range found {
		byAction[permission.Key.ResourceKey+"/"+permission.Key.ActionKey] = permission
	}
	if disabled, ok := byAction["patient_record/delete"]; !ok || disabled.Enabled {
		t.Errorf("patient_record/delete = %+v, want present and disabled", disabled)
	}
	if expired, ok := byAction["medication_request/read"]; !ok || expired.ValidUntil.IsZero() {
		t.Errorf("medication_request/read = %+v, want present with its expiry intact", expired)
	}
}

// The user override read is the same shape of question as the role matrix
// read: everything in force for one lookup key, in one round trip. It adds two
// dimensions the role matrix does not have: hospital scoping, and an optional
// resource instance that narrows a tenant/hospital-wide override further.
func assertActiveUserOverrides(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "user_permission_override")

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	lastYear := now.AddDate(-1, 0, 0)
	nextYear := now.AddDate(1, 0, 0)

	save := func(tenant, hospital, user, action, instance string, effect assignmentstore.OverrideEffect, enabled bool, from, until time.Time) {
		t.Helper()
		if err := store.SaveUserOverride(ctx, assignmentstore.UserOverride{
			Key: assignmentstore.UserOverrideKey{
				TenantID: tenant, HospitalID: hospital, UserExternalID: user,
				ResourceKey: "patient_record", ActionKey: action, ResourceInstanceID: instance,
			},
			Effect: effect, Enabled: enabled, ValidFrom: from, ValidUntil: until, Revision: 7,
		}); err != nil {
			t.Fatalf("saving %s/%s/%s/%s/%s: %v", tenant, hospital, user, action, instance, err)
		}
	}

	// A tenant/hospital-wide revoke, in force.
	save("tenant-a", "hospital-1", "user-1", "update", "", assignmentstore.EffectRevoke, true, lastYear, nextYear)
	// Disabled: present, but INHERIT applies (§8.3), not a grant or a revoke.
	save("tenant-a", "hospital-1", "user-1", "delete", "", assignmentstore.EffectGrant, false, lastYear, nextYear)
	// Expired, so invisible to a decision taken now.
	save("tenant-a", "hospital-1", "user-1", "read", "", assignmentstore.EffectGrant, true, lastYear, now.AddDate(0, -1, 0))
	// Scoped to a resource instance the query below does not ask about.
	save("tenant-a", "hospital-1", "user-1", "read", "patient-999", assignmentstore.EffectGrant, true, lastYear, nextYear)
	// Scoped to the instance the query below does ask about.
	save("tenant-a", "hospital-1", "user-1", "read", "patient-456", assignmentstore.EffectGrant, true, lastYear, nextYear)
	// Another hospital's live grant, which must never reach a hospital-1 decision.
	save("tenant-a", "hospital-2", "user-1", "update", "", assignmentstore.EffectGrant, true, lastYear, nextYear)
	// Another tenant's live grant, same reasoning.
	save("tenant-b", "hospital-1", "user-1", "update", "", assignmentstore.EffectGrant, true, lastYear, nextYear)
	// Another user's live grant.
	save("tenant-a", "hospital-1", "user-2", "update", "", assignmentstore.EffectGrant, true, lastYear, nextYear)

	found, err := store.ActiveUserOverrides(ctx, assignmentstore.ActiveUserOverridesQuery{
		TenantID: "tenant-a", HospitalID: "hospital-1", UserExternalID: "user-1",
		ResourceKey: "patient_record", ResourceInstanceID: "patient-456", At: now,
	})
	if err != nil {
		t.Fatalf("reading the active overrides: %v", err)
	}

	type observed struct {
		effect  assignmentstore.OverrideEffect
		enabled bool
	}
	got := make(map[string]observed, len(found))
	for _, override := range found {
		if override.Key.TenantID != "tenant-a" || override.Key.HospitalID != "hospital-1" {
			t.Errorf("a %s/%s row reached a tenant-a/hospital-1 lookup",
				override.Key.TenantID, override.Key.HospitalID)
		}
		got[override.Key.ActionKey+"/"+override.Key.ResourceInstanceID] =
			observed{override.Effect, override.Enabled}
	}

	want := map[string]observed{
		"update/" + assignmentstore.NoResourceInstance: {assignmentstore.EffectRevoke, true},
		"delete/" + assignmentstore.NoResourceInstance: {assignmentstore.EffectGrant, false},
		"read/patient-456": {assignmentstore.EffectGrant, true},
	}
	if len(got) != len(want) {
		t.Fatalf("the active overrides are %+v, want %+v", got, want)
	}
	for key, want := range want {
		stored, present := got[key]
		if !present {
			t.Errorf("%s is missing from the active overrides %+v", key, got)
			continue
		}
		if stored != want {
			t.Errorf("%s came back %+v, want %+v", key, stored, want)
		}
	}

	// A query for no instance in particular must not pick up the row scoped
	// to patient-456: an instance-scoped override applies only when the
	// decision is actually about that instance.
	wide, err := store.ActiveUserOverrides(ctx, assignmentstore.ActiveUserOverridesQuery{
		TenantID: "tenant-a", HospitalID: "hospital-1", UserExternalID: "user-1",
		ResourceKey: "patient_record", At: now,
	})
	if err != nil {
		t.Fatalf("reading with no instance named: %v", err)
	}
	for _, override := range wide {
		if override.Key.ResourceInstanceID != assignmentstore.NoResourceInstance {
			t.Errorf("an instance-scoped %s row reached a query naming no instance",
				override.Key.ActionKey)
		}
	}
}

// The ADS reports the revision it decided at alongside every decision (§11.3),
// so the revision has to be readable per tenant and has to advance in place
// rather than accumulating a row per change.
func assertPermissionRevision(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "permission_revision")

	if _, found, err := store.PermissionRevision(ctx, "tenant-never-seeded"); err != nil || found {
		t.Fatalf("an unseeded tenant reported found=%t err=%v, want found=false", found, err)
	}

	changed := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	for _, revision := range []int64{184, 185} {
		if err := store.SavePermissionRevision(ctx, assignmentstore.PermissionRevision{
			TenantID:  "tenant-a",
			Revision:  revision,
			ChangedAt: changed,
		}); err != nil {
			t.Fatalf("saving revision %d: %v", revision, err)
		}
	}

	stored, found, err := store.PermissionRevision(ctx, "tenant-a")
	if err != nil || !found {
		t.Fatalf("reading the revision back: found=%t err=%v", found, err)
	}
	if stored.Revision != 185 {
		t.Errorf("revision = %d, want 185", stored.Revision)
	}
	if !stored.ChangedAt.Equal(changed) {
		t.Errorf("changedAt = %s, want %s", stored.ChangedAt, changed)
	}

	// Another tenant's revision is its own.
	if err := store.SavePermissionRevision(ctx, assignmentstore.PermissionRevision{
		TenantID:  "tenant-b",
		Revision:  7,
		ChangedAt: changed,
	}); err != nil {
		t.Fatalf("saving another tenant's revision: %v", err)
	}
	other, found, err := store.PermissionRevision(ctx, "tenant-b")
	if err != nil || !found {
		t.Fatalf("reading the other tenant's revision: found=%t err=%v", found, err)
	}
	if other.Revision != 7 {
		t.Errorf("tenant-b revision = %d, want 7", other.Revision)
	}
}

// assertUniqueKeyColumns requires a unique key or primary key on exactly the
// given columns.
//
// The enforcement cases below write through the adapters, which use an upsert.
// That proves the write path addresses one row, but not that the database would
// refuse a duplicate arriving by another route: Oracle's MERGE needs no unique
// constraint to work, so on that engine an upsert would happily pass over a table
// with no key at all. Reading the catalog closes that gap.
func assertUniqueKeyColumns(t *testing.T, store assignmentstore.Store, table string, want []string) {
	t.Helper()
	defer closeStore(t, store)

	keys, err := store.Schema().UniqueKeys(context.Background(), table)
	if err != nil {
		t.Fatalf("listing unique keys on %s: %v", table, err)
	}

	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)

	for _, columns := range keys {
		got := append([]string(nil), columns...)
		sort.Strings(got)
		if slices.Equal(got, wantSorted) {
			return
		}
	}
	t.Fatalf("no unique key on %s covers exactly %v; found %v", table, want, keys)
}

// Oracle stores the empty string as NULL and PostgreSQL stores it as an empty
// string. Callers must not be able to tell, or every consumer of an optional text
// column would need to know which engine wrote the row.
func assertAbsentOptionalText(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "fhir_resource")

	resource := assignmentstore.Resource{
		ResourceType: "PatientRecord",
		ResourceID:   "patient-empty",
		TenantID:     "tenant-a",
		HospitalID:   "hospital-1",
		Status:       "ACTIVE",
		Department:   "",
		Sensitivity:  "",
		PayloadJSON:  `{"resourceType":"PatientRecord"}`,
		UpdatedAt:    time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC),
	}
	if err := store.SaveResource(ctx, resource); err != nil {
		t.Fatalf("saving a resource with absent optional text: %v", err)
	}

	stored, found, err := store.Resource(ctx, resource.ResourceType, resource.ResourceID)
	if err != nil || !found {
		t.Fatalf("reading it back: found=%t err=%v", found, err)
	}
	if stored.Department != "" {
		t.Errorf("department = %q, want the empty string", stored.Department)
	}
	if stored.Sensitivity != "" {
		t.Errorf("sensitivity = %q, want the empty string", stored.Sensitivity)
	}
	// The mandatory rules read status, so it must survive intact regardless.
	if stored.Status != "ACTIVE" {
		t.Errorf("status = %q, want ACTIVE", stored.Status)
	}
}

// Deleting a resource that was never written must not error: a caller asking
// for an absent instance to be gone has already gotten what it asked for.
func assertDeleteResource(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "fhir_resource")

	resource := assignmentstore.Resource{
		ResourceType: "condition",
		ResourceID:   "condition-delete-1",
		TenantID:     "tenant-a",
		HospitalID:   "hospital-1",
		Status:       "ACTIVE",
		PayloadJSON:  `{"resourceType":"Condition"}`,
		UpdatedAt:    time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC),
	}
	if err := store.SaveResource(ctx, resource); err != nil {
		t.Fatalf("saving the resource to delete: %v", err)
	}

	if err := store.DeleteResource(ctx, resource.ResourceType, resource.ResourceID); err != nil {
		t.Fatalf("deleting the resource: %v", err)
	}
	if _, found, err := store.Resource(ctx, resource.ResourceType, resource.ResourceID); err != nil {
		t.Fatalf("reading after delete: %v", err)
	} else if found {
		t.Error("the resource was still readable after being deleted")
	}

	if err := store.DeleteResource(ctx, resource.ResourceType, resource.ResourceID); err != nil {
		t.Errorf("deleting an already-absent resource errored: %v", err)
	}
}

// A list must stay within its tenant and hospital, within its resource type,
// page in a stable order, and report the total row count separately from the
// page size - a caller cannot otherwise tell whether more pages remain.
func assertListResources(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "fhir_resource")

	save := func(resourceType, id, tenant, hospital string) {
		t.Helper()
		if err := store.SaveResource(ctx, assignmentstore.Resource{
			ResourceType: resourceType,
			ResourceID:   id,
			TenantID:     tenant,
			HospitalID:   hospital,
			Status:       "ACTIVE",
			PayloadJSON:  `{}`,
			UpdatedAt:    time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("saving %s/%s: %v", resourceType, id, err)
		}
	}

	save("condition", "condition-a1", "tenant-a", "hospital-1")
	save("condition", "condition-a2", "tenant-a", "hospital-1")
	save("condition", "condition-a3", "tenant-a", "hospital-1")
	// Another resource type, which the query below must not return.
	save("procedure", "procedure-a1", "tenant-a", "hospital-1")
	// Another hospital in the same tenant, which must not leak into a
	// hospital-1 list.
	save("condition", "condition-a-h2", "tenant-a", "hospital-2")
	// Another tenant entirely.
	save("condition", "condition-b1", "tenant-b", "hospital-1")

	first, total, err := store.ListResources(ctx, assignmentstore.ListResourcesQuery{
		ResourceType: "condition", TenantID: "tenant-a", HospitalID: "hospital-1",
		Limit: 2, Offset: 0,
	})
	if err != nil {
		t.Fatalf("listing the first page: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(first) != 2 {
		t.Fatalf("first page = %d rows, want 2", len(first))
	}
	for _, resource := range first {
		if resource.TenantID != "tenant-a" || resource.HospitalID != "hospital-1" {
			t.Errorf("a %s/%s row reached a tenant-a/hospital-1 list", resource.TenantID, resource.HospitalID)
		}
		if resource.ResourceType != "condition" {
			t.Errorf("a %s row reached a condition list", resource.ResourceType)
		}
	}

	second, _, err := store.ListResources(ctx, assignmentstore.ListResourcesQuery{
		ResourceType: "condition", TenantID: "tenant-a", HospitalID: "hospital-1",
		Limit: 2, Offset: 2,
	})
	if err != nil {
		t.Fatalf("listing the second page: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("second page = %d rows, want 1", len(second))
	}

	seen := map[string]bool{}
	for _, resource := range append(first, second...) {
		if seen[resource.ResourceID] {
			t.Errorf("resource %s appeared on more than one page", resource.ResourceID)
		}
		seen[resource.ResourceID] = true
	}
	if len(seen) != 3 {
		t.Errorf("saw %d distinct resources across both pages, want 3", len(seen))
	}
}

func assertEveryTableExists(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)

	present, err := store.Schema().Tables(context.Background())
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}

	found := make(map[string]bool, len(present))
	for _, name := range present {
		found[name] = true
	}

	var missing []string
	for _, want := range Tables {
		if !found[want] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("tables missing from the migrated schema: %v (found %v)", missing, present)
	}
}

// §8.2: tenant_id + role_external_id + resource_key + action_key is unique. The
// point of asserting through a write rather than through catalog metadata is
// that a key which exists but does not bite would pass the metadata check.
func assertRolePermissionKeyEnforced(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "role_permission")

	permission := assignmentstore.RolePermission{
		Key: assignmentstore.RolePermissionKey{
			TenantID:       "tenant-a",
			RoleExternalID: "role-doctor",
			ResourceKey:    "patient_record",
			ActionKey:      "read",
		},
		Enabled:    true,
		ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidUntil: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		Revision:   1,
	}

	if err := store.SaveRolePermission(ctx, permission); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// The same key with a different value must update in place, not duplicate.
	permission.Enabled = false
	permission.Revision = 2
	if err := store.SaveRolePermission(ctx, permission); err != nil {
		t.Fatalf("second save on the same key: %v", err)
	}

	stored, found, err := store.RolePermission(ctx, permission.Key)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if !found {
		t.Fatal("the saved role permission was not found")
	}
	if stored.Revision != 2 || stored.Enabled {
		t.Errorf("stored = {enabled:%t revision:%d}, want {enabled:false revision:2}",
			stored.Enabled, stored.Revision)
	}
}

// §8.2: tenant + hospital + user + resource + action + instance is unique.
func assertUserOverrideKeyEnforced(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "user_permission_override")

	override := assignmentstore.UserOverride{
		Key: assignmentstore.UserOverrideKey{
			TenantID:           "tenant-a",
			HospitalID:         "hospital-1",
			UserExternalID:     "user-123",
			ResourceKey:        "patient_record",
			ActionKey:          "update",
			ResourceInstanceID: assignmentstore.NoResourceInstance,
		},
		Effect:     assignmentstore.EffectRevoke,
		Enabled:    true,
		ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidUntil: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		Revision:   7,
	}

	if err := store.SaveUserOverride(ctx, override); err != nil {
		t.Fatalf("first save: %v", err)
	}

	override.Effect = assignmentstore.EffectGrant
	override.Revision = 8
	if err := store.SaveUserOverride(ctx, override); err != nil {
		t.Fatalf("second save on the same key: %v", err)
	}

	stored, found, err := store.UserOverride(ctx, override.Key)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if !found {
		t.Fatal("the saved override was not found")
	}
	if stored.Effect != assignmentstore.EffectGrant || stored.Revision != 8 {
		t.Errorf("stored = {effect:%s revision:%d}, want {effect:GRANT revision:8}",
			stored.Effect, stored.Revision)
	}
}

func assertRolePermissionIndex(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)

	assertIndexCovers(t, store, "role_permission",
		[]string{"tenant_id", "role_external_id"})
}

// §8.2 asks for active user overrides to be indexed by tenant, hospital, user and
// validity period. The leading columns alone would be satisfied by the unique key
// that happens to start the same way, so the validity columns are required
// explicitly: without them the "which overrides are in force now" lookup, the one
// the ADS makes on every decision, has nothing behind it.
func assertUserOverrideIndex(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)

	assertIndexCovers(t, store, "user_permission_override",
		[]string{"tenant_id", "hospital_id", "user_external_id"})
	assertIndexIncludes(t, store, "user_permission_override",
		[]string{"tenant_id", "hospital_id", "user_external_id", "valid_from", "valid_until"})
}

// assertIndexIncludes requires one index containing all the given columns, in any
// order. Which order an engine's optimiser prefers is its business; that the
// validity dimension is indexed at all is not.
func assertIndexIncludes(t *testing.T, store assignmentstore.Store, table string, want []string) {
	t.Helper()
	ctx := context.Background()

	indexes, err := store.Schema().Indexes(ctx, table)
	if err != nil {
		t.Fatalf("listing indexes on %s: %v", table, err)
	}

	for _, columns := range indexes {
		if containsAll(columns, want) {
			return
		}
	}
	t.Fatalf("no index on %s covers %v; indexes are %v", table, want, indexes)
}

func containsAll(columns, want []string) bool {
	for _, needed := range want {
		if !slices.Contains(columns, needed) {
			return false
		}
	}
	return true
}

// assertIndexCovers requires some index or unique key whose leading columns are
// exactly the given prefix. Asserting the prefix rather than a name lets either
// engine's optimiser use whichever access path it prefers, while still failing
// if the hot lookup has no supporting index at all.
func assertIndexCovers(t *testing.T, store assignmentstore.Store, table string, prefix []string) {
	t.Helper()
	ctx := context.Background()

	indexes, err := store.Schema().Indexes(ctx, table)
	if err != nil {
		t.Fatalf("listing indexes on %s: %v", table, err)
	}
	uniques, err := store.Schema().UniqueKeys(ctx, table)
	if err != nil {
		t.Fatalf("listing unique keys on %s: %v", table, err)
	}
	for name, columns := range uniques {
		indexes[name] = columns
	}

	for _, columns := range indexes {
		if hasPrefix(columns, prefix) {
			return
		}
	}
	t.Fatalf("no index on %s leads with %v; indexes are %v", table, prefix, indexes)
}

func hasPrefix(columns, prefix []string) bool {
	if len(columns) < len(prefix) {
		return false
	}
	for i, want := range prefix {
		if columns[i] != want {
			return false
		}
	}
	return true
}

// Oracle has no native boolean, so a generic BOOLEAN column becomes NUMBER(1)
// there. Callers must not be able to tell.
func assertBooleanRoundTrip(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "role_permission")

	for _, enabled := range []bool{true, false} {
		key := assignmentstore.RolePermissionKey{
			TenantID:       "tenant-a",
			RoleExternalID: "role-nurse",
			ResourceKey:    "patient_record",
			ActionKey:      map[bool]string{true: "read", false: "delete"}[enabled],
		}
		if err := store.SaveRolePermission(ctx, assignmentstore.RolePermission{
			Key:        key,
			Enabled:    enabled,
			ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			ValidUntil: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
			Revision:   1,
		}); err != nil {
			t.Fatalf("saving enabled=%t: %v", enabled, err)
		}

		stored, found, err := store.RolePermission(ctx, key)
		if err != nil || !found {
			t.Fatalf("reading enabled=%t: found=%t err=%v", enabled, found, err)
		}
		if stored.Enabled != enabled {
			t.Errorf("enabled round-tripped as %t, want %t", stored.Enabled, enabled)
		}
	}
}

// The same instant written from one zone must come back as the same instant,
// whatever the engine stores underneath.
func assertTimestampRoundTrip(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "role_permission")

	// Deliberately not UTC and not a whole hour offset, so a driver that drops
	// the zone or rounds the offset cannot pass by luck.
	kolkata := time.FixedZone("IST", 5*3600+1800)
	validFrom := time.Date(2026, 3, 15, 9, 30, 45, 0, kolkata)

	key := assignmentstore.RolePermissionKey{
		TenantID:       "tenant-a",
		RoleExternalID: "role-doctor",
		ResourceKey:    "patient_record",
		ActionKey:      "read",
	}
	if err := store.SaveRolePermission(ctx, assignmentstore.RolePermission{
		Key:        key,
		Enabled:    true,
		ValidFrom:  validFrom,
		ValidUntil: validFrom.Add(24 * time.Hour),
		Revision:   1,
	}); err != nil {
		t.Fatalf("saving: %v", err)
	}

	stored, found, err := store.RolePermission(ctx, key)
	if err != nil || !found {
		t.Fatalf("reading: found=%t err=%v", found, err)
	}
	if !stored.ValidFrom.Equal(validFrom) {
		t.Errorf("validFrom round-tripped as %s, want the same instant as %s",
			stored.ValidFrom.Format(time.RFC3339Nano), validFrom.Format(time.RFC3339Nano))
	}
	if !stored.ValidUntil.Equal(validFrom.Add(24 * time.Hour)) {
		t.Errorf("validUntil round-tripped as %s, want %s",
			stored.ValidUntil.Format(time.RFC3339Nano),
			validFrom.Add(24*time.Hour).Format(time.RFC3339Nano))
	}
}

// Every JSON-bearing column is where portability quietly fails, so each one is
// written and read back and compared as text, not as a parsed document: a column
// that reorders or reformats what it was given has changed the payload. All six
// are covered - before_json, after_json, the outbox payload, expression_json,
// idp_config and the fhir_resource payload.
func assertJSONRoundTrip(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "permission_audit_event", "outbox_event",
		"ui_capability_definition", "installation_config", "fhir_resource")

	// Key order, unicode, an embedded quote and a deliberately un-pretty layout:
	// all things a JSON-aware column type is entitled to normalise away.
	payload := `{"z":1,"a":{"nested":["x",2,null,true]},"unicode":"ünïcodé — ok","quote":"say \"hi\"","empty":{},"list":[]}`
	created := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

	if err := store.AppendAuditEvent(ctx, assignmentstore.AuditEvent{
		EventID:       "audit-1",
		ActorID:       "admin-1",
		Operation:     "UPDATE",
		TargetType:    "role_permission",
		BeforeJSON:    payload,
		AfterJSON:     `{"enabled":true}`,
		TenantID:      "tenant-a",
		HospitalID:    "hospital-1",
		CorrelationID: "corr-1",
		CreatedAt:     created,
	}); err != nil {
		t.Fatalf("appending an audit event: %v", err)
	}

	audit, found, err := store.AuditEvent(ctx, "audit-1")
	if err != nil || !found {
		t.Fatalf("reading the audit event: found=%t err=%v", found, err)
	}
	if audit.BeforeJSON != payload {
		t.Errorf("before_json round-tripped as\n  %s\nwant\n  %s", audit.BeforeJSON, payload)
	}
	if audit.AfterJSON != `{"enabled":true}` {
		t.Errorf("after_json round-tripped as %s", audit.AfterJSON)
	}
	if !json.Valid([]byte(audit.BeforeJSON)) {
		t.Errorf("before_json came back as invalid JSON: %s", audit.BeforeJSON)
	}

	if err := store.AppendOutboxEvent(ctx, assignmentstore.OutboxEvent{
		EventID:      "outbox-1",
		AggregateKey: "tenant-a:role-doctor",
		EventType:    "permission.changed",
		Payload:      payload,
		CreatedAt:    created,
	}); err != nil {
		t.Fatalf("appending an outbox event: %v", err)
	}

	outbox, found, err := store.OutboxEvent(ctx, "outbox-1")
	if err != nil || !found {
		t.Fatalf("reading the outbox event: found=%t err=%v", found, err)
	}
	if outbox.Payload != payload {
		t.Errorf("outbox payload round-tripped as\n  %s\nwant\n  %s", outbox.Payload, payload)
	}

	if err := store.SaveCapability(ctx, assignmentstore.Capability{
		CapabilityKey:   "patient.record.edit",
		ModuleKey:       "patient",
		ContextType:     "instance",
		ExpressionJSON:  payload,
		CatalogRevision: 3,
		Enabled:         true,
	}); err != nil {
		t.Fatalf("saving a capability: %v", err)
	}

	capability, found, err := store.Capability(ctx, "patient.record.edit")
	if err != nil || !found {
		t.Fatalf("reading the capability: found=%t err=%v", found, err)
	}
	if capability.ExpressionJSON != payload {
		t.Errorf("expression_json round-tripped as\n  %s\nwant\n  %s",
			capability.ExpressionJSON, payload)
	}

	// installation_config.idp_config and fhir_resource.payload are JSON-bearing
	// too, so they get the same treatment as the four the PRD calls out.
	if err := store.SaveInstallationConfig(ctx, assignmentstore.InstallationConfig{
		InstallationID: "installation-1",
		IDPType:        "keycloak",
		IDPConfigJSON:  payload,
		ActiveRootTag:  "v1.0.0",
	}); err != nil {
		t.Fatalf("saving the installation config: %v", err)
	}

	config, found, err := store.InstallationConfig(ctx, "installation-1")
	if err != nil || !found {
		t.Fatalf("reading the installation config: found=%t err=%v", found, err)
	}
	if config.IDPConfigJSON != payload {
		t.Errorf("idp_config round-tripped as\n  %s\nwant\n  %s", config.IDPConfigJSON, payload)
	}

	if err := store.SaveResource(ctx, assignmentstore.Resource{
		ResourceType: "PatientRecord",
		ResourceID:   "patient-456",
		TenantID:     "tenant-a",
		HospitalID:   "hospital-1",
		Status:       "ACTIVE",
		Department:   "cardiology",
		Sensitivity:  "normal",
		PayloadJSON:  payload,
		UpdatedAt:    created,
	}); err != nil {
		t.Fatalf("saving a resource: %v", err)
	}

	resource, found, err := store.Resource(ctx, "PatientRecord", "patient-456")
	if err != nil || !found {
		t.Fatalf("reading the resource: found=%t err=%v", found, err)
	}
	if resource.PayloadJSON != payload {
		t.Errorf("fhir payload round-tripped as\n  %s\nwant\n  %s", resource.PayloadJSON, payload)
	}
	// The attributes the mandatory rules read are columns, not payload fields, so
	// they must survive as columns.
	if resource.Status != "ACTIVE" || resource.Department != "cardiology" {
		t.Errorf("resource attributes round-tripped as {status:%q department:%q}",
			resource.Status, resource.Department)
	}
}

// Oracle stores the empty string as NULL, and NULL never equals NULL in a unique
// index. An override that applies to every instance therefore cannot be
// identified by an empty or absent instance id: without an explicit sentinel the
// §8.2 unique key silently stops enforcing, on both engines.
func assertEmptyResourceInstance(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "user_permission_override")

	base := assignmentstore.UserOverrideKey{
		TenantID:       "tenant-a",
		HospitalID:     "hospital-1",
		UserExternalID: "user-123",
		ResourceKey:    "patient_record",
		ActionKey:      "delete",
	}

	// An empty instance id means "every instance" and must be normalised to the
	// sentinel, so this save and the next one address the same row.
	empty := base
	empty.ResourceInstanceID = ""
	if err := store.SaveUserOverride(ctx, assignmentstore.UserOverride{
		Key: empty, Effect: assignmentstore.EffectRevoke, Enabled: true,
		ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidUntil: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		Revision:   1,
	}); err != nil {
		t.Fatalf("saving with an empty instance id: %v", err)
	}

	sentinel := base
	sentinel.ResourceInstanceID = assignmentstore.NoResourceInstance
	if err := store.SaveUserOverride(ctx, assignmentstore.UserOverride{
		Key: sentinel, Effect: assignmentstore.EffectGrant, Enabled: true,
		ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidUntil: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		Revision:   2,
	}); err != nil {
		t.Fatalf("saving with the sentinel instance id: %v", err)
	}

	// Both spellings must address one row, reachable by either.
	byEmpty, found, err := store.UserOverride(ctx, empty)
	if err != nil || !found {
		t.Fatalf("reading by the empty instance id: found=%t err=%v", found, err)
	}
	if byEmpty.Effect != assignmentstore.EffectGrant || byEmpty.Revision != 2 {
		t.Errorf("an empty and a sentinel instance id addressed different rows: got %+v", byEmpty)
	}
	if byEmpty.Key.ResourceInstanceID != assignmentstore.NoResourceInstance {
		t.Errorf("instance id came back as %q, want the sentinel %q",
			byEmpty.Key.ResourceInstanceID, assignmentstore.NoResourceInstance)
	}

	bySentinel, found, err := store.UserOverride(ctx, sentinel)
	if err != nil || !found {
		t.Fatalf("reading by the sentinel instance id: found=%t err=%v", found, err)
	}
	if bySentinel.Revision != byEmpty.Revision {
		t.Errorf("the two spellings disagree: %d and %d", bySentinel.Revision, byEmpty.Revision)
	}
}

// published_at is genuinely absent until the event is published, which is the
// one place a NULL is the right answer. Oracle and Postgres must agree on it.
func assertNullableTimestamp(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "outbox_event")

	created := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	if err := store.AppendOutboxEvent(ctx, assignmentstore.OutboxEvent{
		EventID:      "outbox-unpublished",
		AggregateKey: "tenant-a:role-doctor",
		EventType:    "permission.changed",
		Payload:      `{"revision":1}`,
		CreatedAt:    created,
	}); err != nil {
		t.Fatalf("appending: %v", err)
	}

	event, found, err := store.OutboxEvent(ctx, "outbox-unpublished")
	if err != nil || !found {
		t.Fatalf("reading: found=%t err=%v", found, err)
	}
	if event.PublishedAt != nil {
		t.Errorf("publishedAt = %v, want no publication time", event.PublishedAt)
	}
	if !event.CreatedAt.Equal(created) {
		t.Errorf("createdAt = %s, want %s", event.CreatedAt, created)
	}
}

// A successful SaveRoleMatrix must leave all four side effects visible: the
// role permission row, the audit event, the outbox event and the advanced
// tenant revision (§9.4, §10.1, §16.1).
func assertSaveRoleMatrixAtomicSuccess(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "role_permission", "permission_audit_event", "outbox_event", "permission_revision")

	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	write := assignmentstore.RoleMatrixWrite{
		TenantID:         "tenant-a",
		RoleExternalID:   "role-doctor",
		ExpectedRevision: 0,
		Permissions: []assignmentstore.RolePermissionInput{
			{ResourceKey: "patient_record", ActionKey: "read", Enabled: true,
				ValidFrom: created.AddDate(-1, 0, 0)},
		},
		Audit: assignmentstore.AuditEvent{
			EventID:       "audit-atomic-1",
			ActorID:       "admin-1",
			Operation:     "ROLE_MATRIX_SAVE",
			TargetType:    "role_permission",
			BeforeJSON:    `{}`,
			AfterJSON:     `{"patient_record.read":true}`,
			HospitalID:    "",
			CorrelationID: "corr-atomic-1",
			CreatedAt:     created,
		},
		Outbox: assignmentstore.OutboxEvent{
			EventID:      "outbox-atomic-1",
			AggregateKey: "tenant-a:role-doctor",
			EventType:    "permission.changed",
			Payload:      `{"revision":1}`,
			CreatedAt:    created,
		},
	}

	newRevision, err := store.SaveRoleMatrix(ctx, write)
	if err != nil {
		t.Fatalf("SaveRoleMatrix: %v", err)
	}
	if newRevision != 1 {
		t.Errorf("newRevision = %d, want 1", newRevision)
	}

	permission, found, err := store.RolePermission(ctx, assignmentstore.RolePermissionKey{
		TenantID: "tenant-a", RoleExternalID: "role-doctor",
		ResourceKey: "patient_record", ActionKey: "read",
	})
	if err != nil || !found {
		t.Fatalf("reading the role permission back: found=%t err=%v", found, err)
	}
	if !permission.Enabled || permission.Revision != 1 {
		t.Errorf("permission = %+v, want enabled=true revision=1", permission)
	}

	audit, found, err := store.AuditEvent(ctx, "audit-atomic-1")
	if err != nil || !found {
		t.Fatalf("reading the audit event back: found=%t err=%v", found, err)
	}
	if audit.TenantID != "tenant-a" || audit.CorrelationID != "corr-atomic-1" {
		t.Errorf("audit = %+v, want tenant-a and corr-atomic-1", audit)
	}

	if _, found, err := store.OutboxEvent(ctx, "outbox-atomic-1"); err != nil || !found {
		t.Fatalf("reading the outbox event back: found=%t err=%v", found, err)
	}

	revision, found, err := store.PermissionRevision(ctx, "tenant-a")
	if err != nil || !found {
		t.Fatalf("reading the permission revision back: found=%t err=%v", found, err)
	}
	if revision.Revision != 1 {
		t.Errorf("tenant revision = %d, want 1", revision.Revision)
	}
}

// A stale ExpectedRevision must fail cleanly and write nothing: not the
// permission, not the audit event, not the outbox event, and the tenant
// revision must not move.
func assertSaveRoleMatrixStaleRevision(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "role_permission", "permission_audit_event", "outbox_event", "permission_revision")

	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	first := assignmentstore.RoleMatrixWrite{
		TenantID:         "tenant-a",
		RoleExternalID:   "role-nurse",
		ExpectedRevision: 0,
		Permissions: []assignmentstore.RolePermissionInput{
			{ResourceKey: "patient_record", ActionKey: "list", Enabled: true, ValidFrom: created},
		},
		Audit:  assignmentstore.AuditEvent{EventID: "audit-stale-1", Operation: "ROLE_MATRIX_SAVE", CreatedAt: created},
		Outbox: assignmentstore.OutboxEvent{EventID: "outbox-stale-1", AggregateKey: "tenant-a:role-nurse", EventType: "permission.changed", Payload: `{}`, CreatedAt: created},
	}
	if _, err := store.SaveRoleMatrix(ctx, first); err != nil {
		t.Fatalf("the first save: %v", err)
	}

	// The tenant is now at revision 1. A second save that still believes
	// revision 0 is current must be rejected.
	stale := assignmentstore.RoleMatrixWrite{
		TenantID:         "tenant-a",
		RoleExternalID:   "role-nurse",
		ExpectedRevision: 0,
		Permissions: []assignmentstore.RolePermissionInput{
			{ResourceKey: "patient_record", ActionKey: "delete", Enabled: true, ValidFrom: created},
		},
		Audit:  assignmentstore.AuditEvent{EventID: "audit-stale-2", Operation: "ROLE_MATRIX_SAVE", CreatedAt: created},
		Outbox: assignmentstore.OutboxEvent{EventID: "outbox-stale-2", AggregateKey: "tenant-a:role-nurse", EventType: "permission.changed", Payload: `{}`, CreatedAt: created},
	}
	if _, err := store.SaveRoleMatrix(ctx, stale); !errors.Is(err, assignmentstore.ErrRevisionConflict) {
		t.Fatalf("SaveRoleMatrix with a stale revision returned %v, want ErrRevisionConflict", err)
	}

	if _, found, err := store.RolePermission(ctx, assignmentstore.RolePermissionKey{
		TenantID: "tenant-a", RoleExternalID: "role-nurse",
		ResourceKey: "patient_record", ActionKey: "delete",
	}); err != nil {
		t.Fatalf("reading the rejected permission: %v", err)
	} else if found {
		t.Error("the rejected write's role permission was written anyway")
	}
	if _, found, err := store.AuditEvent(ctx, "audit-stale-2"); err != nil {
		t.Fatalf("reading the rejected audit event: %v", err)
	} else if found {
		t.Error("the rejected write's audit event was written anyway")
	}
	if _, found, err := store.OutboxEvent(ctx, "outbox-stale-2"); err != nil {
		t.Fatalf("reading the rejected outbox event: %v", err)
	} else if found {
		t.Error("the rejected write's outbox event was written anyway")
	}

	revision, _, err := store.PermissionRevision(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("reading the tenant revision: %v", err)
	}
	if revision.Revision != 1 {
		t.Errorf("tenant revision = %d after a rejected write, want it unchanged at 1", revision.Revision)
	}
}

// Two callers who both read revision 0 and race to save must not both
// succeed: exactly one commits and advances the revision, and the other sees
// its expected revision has gone stale by the time it gets the lock.
func assertSaveRoleMatrixConcurrentWrites(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "role_permission", "permission_audit_event", "outbox_event", "permission_revision")

	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	write := func(eventSuffix string) assignmentstore.RoleMatrixWrite {
		return assignmentstore.RoleMatrixWrite{
			TenantID:         "tenant-a",
			RoleExternalID:   "role-porter",
			ExpectedRevision: 0,
			Permissions: []assignmentstore.RolePermissionInput{
				{ResourceKey: "patient_record", ActionKey: "read", Enabled: true, ValidFrom: created},
			},
			Audit: assignmentstore.AuditEvent{
				EventID: "audit-concurrent-" + eventSuffix, Operation: "ROLE_MATRIX_SAVE", CreatedAt: created,
			},
			Outbox: assignmentstore.OutboxEvent{
				EventID: "outbox-concurrent-" + eventSuffix, AggregateKey: "tenant-a:role-porter",
				EventType: "permission.changed", Payload: `{}`, CreatedAt: created,
			},
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = store.SaveRoleMatrix(ctx, write(fmt.Sprintf("%d", i)))
		}(i)
	}
	close(start)
	wg.Wait()

	successes, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, assignmentstore.ErrRevisionConflict):
			conflicts++
		default:
			t.Fatalf("an unexpected error from a concurrent save: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("got %d successes and %d conflicts, want exactly one of each (errors: %v)",
			successes, conflicts, errs)
	}

	revision, _, err := store.PermissionRevision(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("reading the tenant revision: %v", err)
	}
	if revision.Revision != 1 {
		t.Errorf("tenant revision = %d after one concurrent success, want 1", revision.Revision)
	}
}

// A failure partway through the transaction - forced here by a duplicate
// outbox event id, which the unique primary key refuses - must roll back
// every write that came before it too: the role permission, the audit event
// and the revision bump must all be absent, exactly as if the call had never
// happened.
func assertSaveRoleMatrixRollsBackOnFailure(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "role_permission", "permission_audit_event", "outbox_event", "permission_revision")

	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := store.AppendOutboxEvent(ctx, assignmentstore.OutboxEvent{
		EventID: "outbox-rollback-1", AggregateKey: "pre-existing", EventType: "permission.changed",
		Payload: `{}`, CreatedAt: created,
	}); err != nil {
		t.Fatalf("seeding a conflicting outbox event: %v", err)
	}

	write := assignmentstore.RoleMatrixWrite{
		TenantID:         "tenant-a",
		RoleExternalID:   "role-doctor",
		ExpectedRevision: 0,
		Permissions: []assignmentstore.RolePermissionInput{
			{ResourceKey: "patient_record", ActionKey: "update", Enabled: true, ValidFrom: created},
		},
		Audit: assignmentstore.AuditEvent{EventID: "audit-rollback-1", Operation: "ROLE_MATRIX_SAVE", CreatedAt: created},
		// The same event id as the row seeded above: the unique key forces
		// this insert to fail after the permission and audit writes above
		// it in the transaction have already run.
		Outbox: assignmentstore.OutboxEvent{
			EventID: "outbox-rollback-1", AggregateKey: "tenant-a:role-doctor",
			EventType: "permission.changed", Payload: `{}`, CreatedAt: created,
		},
	}

	if _, err := store.SaveRoleMatrix(ctx, write); err == nil {
		t.Fatal("SaveRoleMatrix succeeded despite a duplicate outbox event id")
	}

	if _, found, err := store.RolePermission(ctx, assignmentstore.RolePermissionKey{
		TenantID: "tenant-a", RoleExternalID: "role-doctor",
		ResourceKey: "patient_record", ActionKey: "update",
	}); err != nil {
		t.Fatalf("reading the role permission: %v", err)
	} else if found {
		t.Error("the role permission was written despite the transaction failing")
	}
	if _, found, err := store.AuditEvent(ctx, "audit-rollback-1"); err != nil {
		t.Fatalf("reading the audit event: %v", err)
	} else if found {
		t.Error("the audit event was written despite the transaction failing")
	}
	if _, found, err := store.PermissionRevision(ctx, "tenant-a"); err != nil {
		t.Fatalf("reading the tenant revision: %v", err)
	} else if found {
		t.Error("the permission revision was seeded despite the transaction failing")
	}
}

// §8.1's audit trail is append-only: saving the same role permission twice
// must leave both audit events readable, not have the second overwrite the
// first, even though the role permission row itself is updated in place.
func assertSaveRoleMatrixAuditHistoryIsAppendOnly(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "role_permission", "permission_audit_event", "outbox_event", "permission_revision")

	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	newRevision, err := store.SaveRoleMatrix(ctx, assignmentstore.RoleMatrixWrite{
		TenantID: "tenant-a", RoleExternalID: "role-doctor", ExpectedRevision: 0,
		Permissions: []assignmentstore.RolePermissionInput{
			{ResourceKey: "patient_record", ActionKey: "update", Enabled: true, ValidFrom: created},
		},
		Audit:  assignmentstore.AuditEvent{EventID: "audit-history-1", Operation: "ROLE_MATRIX_SAVE", BeforeJSON: `{}`, AfterJSON: `{"enabled":true}`, CreatedAt: created},
		Outbox: assignmentstore.OutboxEvent{EventID: "outbox-history-1", AggregateKey: "tenant-a:role-doctor", EventType: "permission.changed", Payload: `{}`, CreatedAt: created},
	})
	if err != nil {
		t.Fatalf("the first save: %v", err)
	}

	if _, err := store.SaveRoleMatrix(ctx, assignmentstore.RoleMatrixWrite{
		TenantID: "tenant-a", RoleExternalID: "role-doctor", ExpectedRevision: newRevision,
		Permissions: []assignmentstore.RolePermissionInput{
			{ResourceKey: "patient_record", ActionKey: "update", Enabled: false, ValidFrom: created},
		},
		Audit:  assignmentstore.AuditEvent{EventID: "audit-history-2", Operation: "ROLE_MATRIX_SAVE", BeforeJSON: `{"enabled":true}`, AfterJSON: `{"enabled":false}`, CreatedAt: created},
		Outbox: assignmentstore.OutboxEvent{EventID: "outbox-history-2", AggregateKey: "tenant-a:role-doctor", EventType: "permission.changed", Payload: `{}`, CreatedAt: created},
	}); err != nil {
		t.Fatalf("the second save: %v", err)
	}

	for _, eventID := range []string{"audit-history-1", "audit-history-2"} {
		if _, found, err := store.AuditEvent(ctx, eventID); err != nil || !found {
			t.Errorf("%s: found=%t err=%v, want it still readable", eventID, found, err)
		}
	}

	permission, found, err := store.RolePermission(ctx, assignmentstore.RolePermissionKey{
		TenantID: "tenant-a", RoleExternalID: "role-doctor",
		ResourceKey: "patient_record", ActionKey: "update",
	})
	if err != nil || !found {
		t.Fatalf("reading the updated permission: found=%t err=%v", found, err)
	}
	if permission.Enabled {
		t.Error("the role permission still reads enabled after the second save disabled it")
	}
}

// The publisher loop drains outbox rows oldest first and must not see a row
// again once it has been marked published.
func assertUnpublishedOutboxEvents(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "outbox_event")

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"outbox-unpub-1", "outbox-unpub-2", "outbox-unpub-3"} {
		if err := store.AppendOutboxEvent(ctx, assignmentstore.OutboxEvent{
			EventID: id, AggregateKey: "tenant-a:role-doctor", EventType: "permission.changed",
			Payload: `{}`, CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("seeding %s: %v", id, err)
		}
	}

	unpublished, err := store.UnpublishedOutboxEvents(ctx, 10)
	if err != nil {
		t.Fatalf("reading unpublished events: %v", err)
	}
	if len(unpublished) != 3 {
		t.Fatalf("got %d unpublished events, want 3", len(unpublished))
	}
	for i, want := range []string{"outbox-unpub-1", "outbox-unpub-2", "outbox-unpub-3"} {
		if unpublished[i].EventID != want {
			t.Errorf("event %d = %s, want %s (oldest first)", i, unpublished[i].EventID, want)
		}
	}

	if err := store.MarkOutboxEventPublished(ctx, "outbox-unpub-2", base.Add(time.Hour)); err != nil {
		t.Fatalf("marking published: %v", err)
	}

	remaining, err := store.UnpublishedOutboxEvents(ctx, 10)
	if err != nil {
		t.Fatalf("reading unpublished events after marking one published: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("got %d unpublished events after marking one published, want 2", len(remaining))
	}
	for _, event := range remaining {
		if event.EventID == "outbox-unpub-2" {
			t.Error("a published event was still reported as unpublished")
		}
	}

	published, found, err := store.OutboxEvent(ctx, "outbox-unpub-2")
	if err != nil || !found {
		t.Fatalf("reading the published event back: found=%t err=%v", found, err)
	}
	if published.PublishedAt == nil {
		t.Error("publishedAt is still nil after MarkOutboxEventPublished")
	}
}

// A successful SaveUserOverrideWrite for a GRANT or REVOKE must leave all
// four side effects visible: the override row (with its reason), the audit
// event, the outbox event and the advanced tenant revision (§9.3, §9.4,
// §10.1) - the same atomicity SaveRoleMatrix gives role permissions.
func assertSaveUserOverrideWriteAtomicSuccess(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "user_permission_override", "permission_audit_event", "outbox_event", "permission_revision")

	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	write := assignmentstore.UserOverrideWrite{
		Key: assignmentstore.UserOverrideKey{
			TenantID: "tenant-a", HospitalID: "hospital-1", UserExternalID: "user-1",
			ResourceKey: "patient_record", ActionKey: "delete",
		},
		Effect:           assignmentstore.EffectRevoke,
		Reason:           "clinician under investigation",
		ValidFrom:        created.AddDate(-1, 0, 0),
		ExpectedRevision: 0,
		Audit: assignmentstore.AuditEvent{
			EventID:       "audit-override-atomic-1",
			ActorID:       "admin-1",
			Operation:     "USER_OVERRIDE_SAVE",
			TargetType:    "user_permission_override",
			BeforeJSON:    `{}`,
			AfterJSON:     `{"patient_record.delete":"REVOKE"}`,
			CorrelationID: "corr-override-atomic-1",
			CreatedAt:     created,
		},
		Outbox: assignmentstore.OutboxEvent{
			EventID:      "outbox-override-atomic-1",
			AggregateKey: "tenant-a:user-1",
			EventType:    "permission.changed",
			Payload:      `{"revision":1}`,
			CreatedAt:    created,
		},
	}

	newRevision, err := store.SaveUserOverrideWrite(ctx, write)
	if err != nil {
		t.Fatalf("SaveUserOverrideWrite: %v", err)
	}
	if newRevision != 1 {
		t.Errorf("newRevision = %d, want 1", newRevision)
	}

	override, found, err := store.UserOverride(ctx, write.Key)
	if err != nil || !found {
		t.Fatalf("reading the override back: found=%t err=%v", found, err)
	}
	if override.Effect != assignmentstore.EffectRevoke || !override.Enabled || override.Revision != 1 {
		t.Errorf("override = %+v, want REVOKE enabled=true revision=1", override)
	}
	if override.Reason != "clinician under investigation" {
		t.Errorf("override.Reason = %q, want the write's reason", override.Reason)
	}

	if _, found, err := store.AuditEvent(ctx, "audit-override-atomic-1"); err != nil || !found {
		t.Fatalf("reading the audit event back: found=%t err=%v", found, err)
	}
	if _, found, err := store.OutboxEvent(ctx, "outbox-override-atomic-1"); err != nil || !found {
		t.Fatalf("reading the outbox event back: found=%t err=%v", found, err)
	}

	revision, found, err := store.PermissionRevision(ctx, "tenant-a")
	if err != nil || !found {
		t.Fatalf("reading the permission revision back: found=%t err=%v", found, err)
	}
	if revision.Revision != 1 {
		t.Errorf("tenant revision = %d, want 1", revision.Revision)
	}
}

// A stale ExpectedRevision must fail cleanly and write nothing: not the
// override, not the audit event, not the outbox event, and the tenant
// revision must not move - the same guarantee SaveRoleMatrix gives.
func assertSaveUserOverrideWriteStaleRevision(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "user_permission_override", "permission_audit_event", "outbox_event", "permission_revision")

	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	key := assignmentstore.UserOverrideKey{
		TenantID: "tenant-a", HospitalID: "hospital-1", UserExternalID: "user-2",
		ResourceKey: "patient_record", ActionKey: "update",
	}
	first := assignmentstore.UserOverrideWrite{
		Key: key, Effect: assignmentstore.EffectGrant, Reason: "temporary cover",
		ValidFrom: created, ExpectedRevision: 0,
		Audit:  assignmentstore.AuditEvent{EventID: "audit-override-stale-1", Operation: "USER_OVERRIDE_SAVE", CreatedAt: created},
		Outbox: assignmentstore.OutboxEvent{EventID: "outbox-override-stale-1", AggregateKey: "tenant-a:user-2", EventType: "permission.changed", Payload: `{}`, CreatedAt: created},
	}
	if _, err := store.SaveUserOverrideWrite(ctx, first); err != nil {
		t.Fatalf("the first write: %v", err)
	}

	// The tenant is now at revision 1. A second write that still believes
	// revision 0 is current must be rejected.
	stale := assignmentstore.UserOverrideWrite{
		Key: key, Effect: assignmentstore.EffectRevoke, Reason: "changed my mind",
		ValidFrom: created, ExpectedRevision: 0,
		Audit:  assignmentstore.AuditEvent{EventID: "audit-override-stale-2", Operation: "USER_OVERRIDE_SAVE", CreatedAt: created},
		Outbox: assignmentstore.OutboxEvent{EventID: "outbox-override-stale-2", AggregateKey: "tenant-a:user-2", EventType: "permission.changed", Payload: `{}`, CreatedAt: created},
	}
	if _, err := store.SaveUserOverrideWrite(ctx, stale); !errors.Is(err, assignmentstore.ErrRevisionConflict) {
		t.Fatalf("SaveUserOverrideWrite with a stale revision returned %v, want ErrRevisionConflict", err)
	}

	override, found, err := store.UserOverride(ctx, key)
	if err != nil || !found {
		t.Fatalf("reading the override back: found=%t err=%v", found, err)
	}
	if override.Effect != assignmentstore.EffectGrant {
		t.Errorf("override.Effect = %s, want the first write's GRANT to survive the rejected second write", override.Effect)
	}
	if _, found, err := store.AuditEvent(ctx, "audit-override-stale-2"); err != nil {
		t.Fatalf("reading the rejected audit event: %v", err)
	} else if found {
		t.Error("the rejected write's audit event was written anyway")
	}
	if _, found, err := store.OutboxEvent(ctx, "outbox-override-stale-2"); err != nil {
		t.Fatalf("reading the rejected outbox event: %v", err)
	} else if found {
		t.Error("the rejected write's outbox event was written anyway")
	}
}

// SaveUserOverrideWrite with Effect left at its zero value is INHERIT: it
// must clear an existing row rather than upsert one carrying no effect
// (§8.3: INHERIT is the absence of a row, not a storable effect), and the
// role result then applies unmodified.
func assertSaveUserOverrideWriteInheritClearsTheRow(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	defer closeStore(t, store)
	ctx := context.Background()
	truncate(t, store, "user_permission_override", "permission_audit_event", "outbox_event", "permission_revision")

	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	key := assignmentstore.UserOverrideKey{
		TenantID: "tenant-a", HospitalID: "hospital-1", UserExternalID: "user-3",
		ResourceKey: "patient_record", ActionKey: "read",
	}
	grant := assignmentstore.UserOverrideWrite{
		Key: key, Effect: assignmentstore.EffectGrant, Reason: "exceptional access",
		ValidFrom: created, ExpectedRevision: 0,
		Audit:  assignmentstore.AuditEvent{EventID: "audit-override-inherit-1", Operation: "USER_OVERRIDE_SAVE", CreatedAt: created},
		Outbox: assignmentstore.OutboxEvent{EventID: "outbox-override-inherit-1", AggregateKey: "tenant-a:user-3", EventType: "permission.changed", Payload: `{}`, CreatedAt: created},
	}
	if _, err := store.SaveUserOverrideWrite(ctx, grant); err != nil {
		t.Fatalf("the grant write: %v", err)
	}

	inherit := assignmentstore.UserOverrideWrite{
		Key: key, ExpectedRevision: 1,
		Audit:  assignmentstore.AuditEvent{EventID: "audit-override-inherit-2", Operation: "USER_OVERRIDE_SAVE", CreatedAt: created},
		Outbox: assignmentstore.OutboxEvent{EventID: "outbox-override-inherit-2", AggregateKey: "tenant-a:user-3", EventType: "permission.changed", Payload: `{}`, CreatedAt: created},
	}
	newRevision, err := store.SaveUserOverrideWrite(ctx, inherit)
	if err != nil {
		t.Fatalf("the inherit write: %v", err)
	}
	if newRevision != 2 {
		t.Errorf("newRevision = %d, want 2", newRevision)
	}

	if _, found, err := store.UserOverride(ctx, key); err != nil {
		t.Fatalf("reading the cleared override: %v", err)
	} else if found {
		t.Error("the override row still exists after an INHERIT write")
	}
}

func truncate(t *testing.T, store assignmentstore.Store, tables ...string) {
	t.Helper()
	if err := store.Truncate(context.Background(), tables...); err != nil {
		t.Fatalf("truncating %v: %v", tables, err)
	}
}

func closeStore(t *testing.T, store assignmentstore.Store) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Errorf("closing the store: %v", err)
	}
}
