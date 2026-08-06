package useroverride_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/useroverride"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissionevents"
)

type fakeStore struct {
	rolePermissions []assignmentstore.RolePermission
	overrides       map[string]assignmentstore.UserOverride
	revisions       map[string]int64
	lastWrite       assignmentstore.UserOverrideWrite
	saveErr         error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		overrides: map[string]assignmentstore.UserOverride{},
		revisions: map[string]int64{},
	}
}

func overrideKeyString(key assignmentstore.UserOverrideKey) string {
	key = assignmentstore.NormalizeOverrideKey(key)
	return key.TenantID + "/" + key.HospitalID + "/" + key.UserExternalID + "/" +
		key.ResourceKey + "/" + key.ActionKey + "/" + key.ResourceInstanceID
}

func (f *fakeStore) ActiveRolePermissions(_ context.Context, query assignmentstore.ActiveRolePermissionQuery) ([]assignmentstore.RolePermission, error) {
	roles := make(map[string]bool, len(query.RoleExternalIDs))
	for _, role := range query.RoleExternalIDs {
		roles[role] = true
	}
	var matched []assignmentstore.RolePermission
	for _, permission := range f.rolePermissions {
		if permission.Key.TenantID == query.TenantID &&
			permission.Key.ResourceKey == query.ResourceKey &&
			roles[permission.Key.RoleExternalID] {
			matched = append(matched, permission)
		}
	}
	return matched, nil
}

func (f *fakeStore) ActiveUserOverrides(_ context.Context, query assignmentstore.ActiveUserOverridesQuery) ([]assignmentstore.UserOverride, error) {
	var matched []assignmentstore.UserOverride
	for _, override := range f.overrides {
		key := override.Key
		if key.TenantID == query.TenantID && key.HospitalID == query.HospitalID &&
			key.UserExternalID == query.UserExternalID && key.ResourceKey == query.ResourceKey &&
			!query.At.Before(override.ValidFrom) &&
			(override.ValidUntil.IsZero() || query.At.Before(override.ValidUntil)) {
			matched = append(matched, override)
		}
	}
	return matched, nil
}

func (f *fakeStore) SaveUserOverrideWrite(_ context.Context, write assignmentstore.UserOverrideWrite) (int64, error) {
	f.lastWrite = write
	if f.saveErr != nil {
		return 0, f.saveErr
	}
	current := f.revisions[write.Key.TenantID]
	if write.ExpectedRevision != current {
		return 0, assignmentstore.ErrRevisionConflict
	}
	newRevision := current + 1
	f.revisions[write.Key.TenantID] = newRevision

	key := assignmentstore.NormalizeOverrideKey(write.Key)
	if write.Effect == "" {
		delete(f.overrides, overrideKeyString(key))
	} else {
		f.overrides[overrideKeyString(key)] = assignmentstore.UserOverride{
			Key: key, Effect: write.Effect, Enabled: true,
			ValidFrom: write.ValidFrom, ValidUntil: write.ValidUntil,
			Revision: newRevision, Reason: write.Reason,
		}
	}
	return newRevision, nil
}

type fakeCatalog struct {
	known map[string]bool
}

func newFakeCatalog(pairs ...string) *fakeCatalog {
	known := make(map[string]bool, len(pairs))
	for _, pair := range pairs {
		known[pair] = true
	}
	return &fakeCatalog{known: known}
}

func (c *fakeCatalog) Has(resource, action string) bool {
	return c.known[resource+":"+action]
}

func newHandler(store *fakeStore, catalog *fakeCatalog) *useroverride.Handler {
	counter := 0
	return &useroverride.Handler{
		Store:           store,
		Catalog:         catalog,
		HighRiskActions: []string{"delete"},
		Now:             func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) },
		NewEventID: func() string {
			counter++
			return "evt-" + string(rune('0'+counter))
		},
	}
}

func saveRequest(t *testing.T, identity tokenauth.Identity, tenant, hospital, user string, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling the request body: %v", err)
	}
	path := "/admin/authz/tenants/" + tenant + "/hospitals/" + hospital + "/users/" + user + "/overrides"
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(raw))
	req.SetPathValue("tenant", tenant)
	req.SetPathValue("hospital", hospital)
	req.SetPathValue("user", user)
	return req.WithContext(tokenauth.WithIdentity(req.Context(), identity))
}

func readRequest(t *testing.T, identity tokenauth.Identity, tenant, hospital, user, resource string) *http.Request {
	t.Helper()
	path := "/admin/authz/tenants/" + tenant + "/hospitals/" + hospital + "/users/" + user + "/overrides?resource=" + resource
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetPathValue("tenant", tenant)
	req.SetPathValue("hospital", hospital)
	req.SetPathValue("user", user)
	return req.WithContext(tokenauth.WithIdentity(req.Context(), identity))
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the response body %q: %v", rec.Body.String(), err)
	}
	return body
}

func adminOf(tenant, hospital string) tokenauth.Identity {
	return tokenauth.Identity{PrincipalID: "admin-1", TenantID: tenant, HospitalID: hospital}
}

// A GRANT and a REVOKE must persist with the reason, validity start and
// optional expiry the request named (§9.3, AC "GRANT and REVOKE persist
// with reason, validity start and optional expiry").
func TestSaveGrantPersistsWithReasonValidityAndExpiry(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := saveRequest(t, adminOf("tenant-a", "hospital-1"), "tenant-a", "hospital-1", "user-1", map[string]any{
		"expectedRevision": 0,
		"resourceKey":      "patient_record",
		"actionKey":        "read",
		"effect":           "GRANT",
		"reason":           "temporary clinical cover",
		"validFrom":        "2026-01-01T00:00:00Z",
		"validUntil":       "2026-12-31T00:00:00Z",
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if store.lastWrite.Effect != assignmentstore.EffectGrant {
		t.Errorf("write.Effect = %s, want GRANT", store.lastWrite.Effect)
	}
	if store.lastWrite.Reason != "temporary clinical cover" {
		t.Errorf("write.Reason = %q, want the request's reason", store.lastWrite.Reason)
	}
	if !store.lastWrite.ValidFrom.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("write.ValidFrom = %s, want 2026-01-01", store.lastWrite.ValidFrom)
	}
	if !store.lastWrite.ValidUntil.Equal(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("write.ValidUntil = %s, want 2026-12-31", store.lastWrite.ValidUntil)
	}
}

// Setting INHERIT must clear any existing override row so the role result
// applies unmodified (AC "Setting INHERIT removes or disables the override
// row and the role result applies").
func TestSaveInheritClearsTheOverrideAndTheRoleResultApplies(t *testing.T) {
	store := newFakeStore()
	store.rolePermissions = []assignmentstore.RolePermission{
		{Key: assignmentstore.RolePermissionKey{TenantID: "tenant-a", RoleExternalID: "role-doctor", ResourceKey: "patient_record", ActionKey: "read"}, Enabled: true},
	}
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	// Seed an existing REVOKE, then clear it with INHERIT.
	store.overrides[overrideKeyString(assignmentstore.UserOverrideKey{
		TenantID: "tenant-a", HospitalID: "hospital-1", UserExternalID: "user-1",
		ResourceKey: "patient_record", ActionKey: "read",
	})] = assignmentstore.UserOverride{Effect: assignmentstore.EffectRevoke, Enabled: true, Revision: 1}
	store.revisions["tenant-a"] = 1

	req := saveRequest(t, adminOf("tenant-a", "hospital-1"), "tenant-a", "hospital-1", "user-1", map[string]any{
		"expectedRevision": 1,
		"resourceKey":      "patient_record",
		"actionKey":        "read",
		"effect":           "INHERIT",
		"roleExternalIds":  []string{"role-doctor"},
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if store.lastWrite.Effect != "" {
		t.Errorf("write.Effect = %q, want the zero value for INHERIT", store.lastWrite.Effect)
	}
	body := decodeResponse(t, rec)
	if body["effectiveResult"] != true {
		t.Errorf("effectiveResult = %v, want true (the role result applies)", body["effectiveResult"])
	}

	if _, found, _ := getOverride(store, "tenant-a", "hospital-1", "user-1", "patient_record", "read"); found {
		t.Error("the override row still exists after an INHERIT save")
	}
}

func getOverride(store *fakeStore, tenant, hospital, user, resource, action string) (assignmentstore.UserOverride, bool, error) {
	override, found := store.overrides[overrideKeyString(assignmentstore.UserOverrideKey{
		TenantID: tenant, HospitalID: hospital, UserExternalID: user,
		ResourceKey: resource, ActionKey: action,
	})]
	return override, found, nil
}

// A GRANT or REVOKE with no reason must be rejected before any write (AC
// "An override without a reason is rejected").
func TestSaveRejectsAGrantWithNoReason(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := saveRequest(t, adminOf("tenant-a", "hospital-1"), "tenant-a", "hospital-1", "user-1", map[string]any{
		"expectedRevision": 0,
		"resourceKey":      "patient_record",
		"actionKey":        "read",
		"effect":           "GRANT",
		"validFrom":        "2026-01-01T00:00:00Z",
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if store.revisions["tenant-a"] != 0 {
		t.Error("a write happened despite the missing reason")
	}
}

// A high-risk action with no ValidUntil must default to a bounded expiry
// rather than being left unbounded (AC "A high-risk action without an
// expiry defaults to a bounded expiry rather than being unbounded").
func TestSaveDefaultsAHighRiskActionWithNoExpiryToABoundedExpiry(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog("patient_record:delete")
	handler := newHandler(store, catalog)
	handler.DefaultHighRiskValidity = 30 * 24 * time.Hour

	req := saveRequest(t, adminOf("tenant-a", "hospital-1"), "tenant-a", "hospital-1", "user-1", map[string]any{
		"expectedRevision": 0,
		"resourceKey":      "patient_record",
		"actionKey":        "delete",
		"effect":           "GRANT",
		"reason":           "one-off cleanup task",
		"validFrom":        "2026-01-01T00:00:00Z",
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	want := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	if !store.lastWrite.ValidUntil.Equal(want) {
		t.Errorf("write.ValidUntil = %s, want %s (validFrom + the bounded default)", store.lastWrite.ValidUntil, want)
	}
}

// A non-high-risk action with no ValidUntil must stay unbounded: the
// default only applies to the actions the operator has named as high-risk.
func TestSaveLeavesANonHighRiskGrantWithNoExpiryUnbounded(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := saveRequest(t, adminOf("tenant-a", "hospital-1"), "tenant-a", "hospital-1", "user-1", map[string]any{
		"expectedRevision": 0,
		"resourceKey":      "patient_record",
		"actionKey":        "read",
		"effect":           "GRANT",
		"reason":           "ongoing cover",
		"validFrom":        "2026-01-01T00:00:00Z",
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !store.lastWrite.ValidUntil.IsZero() {
		t.Errorf("write.ValidUntil = %s, want unbounded (zero)", store.lastWrite.ValidUntil)
	}
}

// The response must report both the underlying role result and the
// resulting effective result (AC "The response reports both the role
// result and the effective result").
func TestSaveResponseReportsBothTheRoleResultAndTheEffectiveResult(t *testing.T) {
	store := newFakeStore()
	store.rolePermissions = []assignmentstore.RolePermission{
		{Key: assignmentstore.RolePermissionKey{TenantID: "tenant-a", RoleExternalID: "role-doctor", ResourceKey: "patient_record", ActionKey: "delete"}, Enabled: true},
	}
	catalog := newFakeCatalog("patient_record:delete")
	handler := newHandler(store, catalog)

	req := saveRequest(t, adminOf("tenant-a", "hospital-1"), "tenant-a", "hospital-1", "user-1", map[string]any{
		"expectedRevision": 0,
		"resourceKey":      "patient_record",
		"actionKey":        "delete",
		"effect":           "REVOKE",
		"reason":           "clinician under investigation",
		"validFrom":        "2026-01-01T00:00:00Z",
		"roleExternalIds":  []string{"role-doctor"},
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	body := decodeResponse(t, rec)
	if body["roleResult"] != true {
		t.Errorf("roleResult = %v, want true (role-doctor grants delete)", body["roleResult"])
	}
	if body["effectiveResult"] != false {
		t.Errorf("effectiveResult = %v, want false (REVOKE defeats the role grant)", body["effectiveResult"])
	}
}

// An override that merely duplicates the existing role result must be
// flagged as having no practical effect (AC "An override duplicating the
// existing role result is flagged as having no practical effect").
func TestSaveFlagsAGrantThatDuplicatesAnExistingRoleGrant(t *testing.T) {
	store := newFakeStore()
	store.rolePermissions = []assignmentstore.RolePermission{
		{Key: assignmentstore.RolePermissionKey{TenantID: "tenant-a", RoleExternalID: "role-doctor", ResourceKey: "patient_record", ActionKey: "read"}, Enabled: true},
	}
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := saveRequest(t, adminOf("tenant-a", "hospital-1"), "tenant-a", "hospital-1", "user-1", map[string]any{
		"expectedRevision": 0,
		"resourceKey":      "patient_record",
		"actionKey":        "read",
		"effect":           "GRANT",
		"reason":           "belt and suspenders",
		"validFrom":        "2026-01-01T00:00:00Z",
		"roleExternalIds":  []string{"role-doctor"},
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	body := decodeResponse(t, rec)
	if body["noPracticalEffect"] != true {
		t.Errorf("noPracticalEffect = %v, want true (the grant duplicates the role result)", body["noPracticalEffect"])
	}
}

// A REVOKE that defeats an existing role grant is not a no-op.
func TestSaveDoesNotFlagARevokeThatActuallyChangesTheOutcome(t *testing.T) {
	store := newFakeStore()
	store.rolePermissions = []assignmentstore.RolePermission{
		{Key: assignmentstore.RolePermissionKey{TenantID: "tenant-a", RoleExternalID: "role-doctor", ResourceKey: "patient_record", ActionKey: "read"}, Enabled: true},
	}
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := saveRequest(t, adminOf("tenant-a", "hospital-1"), "tenant-a", "hospital-1", "user-1", map[string]any{
		"expectedRevision": 0,
		"resourceKey":      "patient_record",
		"actionKey":        "read",
		"effect":           "REVOKE",
		"reason":           "actually removes access",
		"validFrom":        "2026-01-01T00:00:00Z",
		"roleExternalIds":  []string{"role-doctor"},
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	body := decodeResponse(t, rec)
	if body["noPracticalEffect"] != false {
		t.Errorf("noPracticalEffect = %v, want false (the revoke changes the outcome)", body["noPracticalEffect"])
	}
}

// Overrides are hospital-scoped: a token scoped to a different hospital
// must be refused (AC "Overrides are hospital-scoped and cannot be written
// across tenant or hospital authority").
func TestSaveRejectsAWriteAcrossHospitalAuthority(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := saveRequest(t, adminOf("tenant-a", "hospital-2"), "tenant-a", "hospital-1", "user-1", map[string]any{
		"expectedRevision": 0,
		"resourceKey":      "patient_record",
		"actionKey":        "read",
		"effect":           "GRANT",
		"reason":           "should never reach the store",
		"validFrom":        "2026-01-01T00:00:00Z",
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if store.revisions["tenant-a"] != 0 {
		t.Error("a write happened despite the hospital mismatch")
	}
}

// Overrides are hospital-scoped: a token scoped to a different tenant must
// also be refused, even for a matching hospital id.
func TestSaveRejectsAWriteAcrossTenantAuthority(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := saveRequest(t, adminOf("tenant-b", "hospital-1"), "tenant-a", "hospital-1", "user-1", map[string]any{
		"expectedRevision": 0,
		"resourceKey":      "patient_record",
		"actionKey":        "read",
		"effect":           "GRANT",
		"reason":           "should never reach the store",
		"validFrom":        "2026-01-01T00:00:00Z",
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

// A stale expected revision must be reported as a conflict, with nothing
// written.
func TestSaveReportsAConflictOnAStaleExpectedRevision(t *testing.T) {
	store := newFakeStore()
	store.revisions["tenant-a"] = 1
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := saveRequest(t, adminOf("tenant-a", "hospital-1"), "tenant-a", "hospital-1", "user-1", map[string]any{
		"expectedRevision": 0,
		"resourceKey":      "patient_record",
		"actionKey":        "read",
		"effect":           "GRANT",
		"reason":           "stale",
		"validFrom":        "2026-01-01T00:00:00Z",
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

// The outbox payload must carry a PermissionChanged event a consumer can
// invalidate from directly, scoped to the user (§10.2).
func TestSaveAppendsAPermissionChangedEventScopedToTheUser(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := saveRequest(t, adminOf("tenant-a", "hospital-1"), "tenant-a", "hospital-1", "user-1", map[string]any{
		"expectedRevision": 0,
		"resourceKey":      "patient_record",
		"actionKey":        "read",
		"effect":           "REVOKE",
		"reason":           "policy violation",
		"validFrom":        "2026-01-01T00:00:00Z",
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var events []permissionevents.PermissionChanged
	if err := json.Unmarshal([]byte(store.lastWrite.Outbox.Payload), &events); err != nil {
		t.Fatalf("decoding the outbox payload %q: %v", store.lastWrite.Outbox.Payload, err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	event := events[0]
	if event.SubjectType != permissionevents.SubjectUser || event.SubjectID != "user-1" {
		t.Errorf("event subject = %s/%s, want USER/user-1", event.SubjectType, event.SubjectID)
	}
	if event.TenantID != "tenant-a" || event.HospitalID != "hospital-1" {
		t.Errorf("event scope = %s/%s, want tenant-a/hospital-1", event.TenantID, event.HospitalID)
	}
	if event.Enabled {
		t.Error("event.Enabled = true, want false for a REVOKE with no surviving role grant")
	}
}

// Read must return the active overrides for the named resource, and must
// not return an override outside its validity window (AC "An expired
// override stops taking effect without any further administrative
// action").
func TestReadReturnsActiveOverridesAndExcludesAnExpiredOne(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog()
	handler := newHandler(store, catalog)

	store.overrides["live"] = assignmentstore.UserOverride{
		Key: assignmentstore.UserOverrideKey{
			TenantID: "tenant-a", HospitalID: "hospital-1", UserExternalID: "user-1",
			ResourceKey: "patient_record", ActionKey: "read", ResourceInstanceID: assignmentstore.NoResourceInstance,
		},
		Effect: assignmentstore.EffectGrant, Enabled: true,
		ValidFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Revision:  1, Reason: "ongoing cover",
	}
	store.overrides["expired"] = assignmentstore.UserOverride{
		Key: assignmentstore.UserOverrideKey{
			TenantID: "tenant-a", HospitalID: "hospital-1", UserExternalID: "user-1",
			ResourceKey: "patient_record", ActionKey: "update", ResourceInstanceID: assignmentstore.NoResourceInstance,
		},
		Effect: assignmentstore.EffectGrant, Enabled: true,
		ValidFrom:  time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidUntil: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		Revision:   1, Reason: "expired long ago",
	}

	req := readRequest(t, adminOf("tenant-a", "hospital-1"), "tenant-a", "hospital-1", "user-1", "patient_record")
	rec := httptest.NewRecorder()
	handler.Read(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeResponse(t, rec)
	overrides, ok := body["overrides"].([]any)
	if !ok {
		t.Fatalf("overrides is not a list: %v", body["overrides"])
	}
	if len(overrides) != 1 {
		t.Fatalf("got %d overrides, want 1 (the expired one excluded)", len(overrides))
	}
	first := overrides[0].(map[string]any)
	if first["actionKey"] != "read" {
		t.Errorf("actionKey = %v, want read", first["actionKey"])
	}
}

// Read must reject a request naming no resource.
func TestReadRejectsARequestWithNoResourceParameter(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog()
	handler := newHandler(store, catalog)

	req := readRequest(t, adminOf("tenant-a", "hospital-1"), "tenant-a", "hospital-1", "user-1", "")
	rec := httptest.NewRecorder()
	handler.Read(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
