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
	"slices"
	"sort"
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
