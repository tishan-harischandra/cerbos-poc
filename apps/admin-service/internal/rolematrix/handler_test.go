package rolematrix_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/rolematrix"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissionevents"
)

type fakeStore struct {
	permissions map[string]assignmentstore.RolePermission
	revisions   map[string]int64
	lastWrite   assignmentstore.RoleMatrixWrite
	saveErr     error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		permissions: map[string]assignmentstore.RolePermission{},
		revisions:   map[string]int64{},
	}
}

func permissionKeyString(key assignmentstore.RolePermissionKey) string {
	return key.TenantID + "/" + key.RoleExternalID + "/" + key.ResourceKey + "/" + key.ActionKey
}

func (f *fakeStore) RolePermission(_ context.Context, key assignmentstore.RolePermissionKey) (assignmentstore.RolePermission, bool, error) {
	permission, found := f.permissions[permissionKeyString(key)]
	return permission, found, nil
}

func (f *fakeStore) RolePermissionsForRole(_ context.Context, tenantID, roleExternalID string) ([]assignmentstore.RolePermission, error) {
	var found []assignmentstore.RolePermission
	for _, permission := range f.permissions {
		if permission.Key.TenantID == tenantID && permission.Key.RoleExternalID == roleExternalID {
			found = append(found, permission)
		}
	}
	return found, nil
}

func (f *fakeStore) SaveRoleMatrix(_ context.Context, write assignmentstore.RoleMatrixWrite) (int64, error) {
	f.lastWrite = write
	if f.saveErr != nil {
		return 0, f.saveErr
	}
	current := f.revisions[write.TenantID]
	if write.ExpectedRevision != current {
		return 0, assignmentstore.ErrRevisionConflict
	}
	newRevision := current + 1
	f.revisions[write.TenantID] = newRevision
	for _, permission := range write.Permissions {
		key := assignmentstore.RolePermissionKey{
			TenantID: write.TenantID, RoleExternalID: write.RoleExternalID,
			ResourceKey: permission.ResourceKey, ActionKey: permission.ActionKey,
		}
		f.permissions[permissionKeyString(key)] = assignmentstore.RolePermission{
			Key: key, Enabled: permission.Enabled, ValidFrom: permission.ValidFrom,
			ValidUntil: permission.ValidUntil, Revision: newRevision,
		}
	}
	return newRevision, nil
}

func (f *fakeStore) PermissionRevision(_ context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error) {
	revision, found := f.revisions[tenantID]
	if !found {
		return assignmentstore.PermissionRevision{}, false, nil
	}
	return assignmentstore.PermissionRevision{TenantID: tenantID, Revision: revision}, true, nil
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

func newHandler(store rolematrix.Store, catalog rolematrix.Catalog) *rolematrix.Handler {
	counter := 0
	return &rolematrix.Handler{
		Store:   store,
		Catalog: catalog,
		Now:     func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) },
		NewEventID: func() string {
			counter++
			return "evt-" + string(rune('0'+counter))
		},
	}
}

func request(t *testing.T, identity tokenauth.Identity, tenant, role string, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling the request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/admin/authz/tenants/"+tenant+"/roles/"+role+"/permissions", bytes.NewReader(raw))
	req.SetPathValue("tenant", tenant)
	req.SetPathValue("role", role)
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

func adminOf(tenant string) tokenauth.Identity {
	return tokenauth.Identity{PrincipalID: "admin-1", TenantID: tenant}
}

// A first save for a role that has never been saved (expectedRevision 0)
// commits and returns revision 1.
func TestSaveCommitsAFirstWriteAndReturnsTheNewRevision(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := request(t, adminOf("tenant-a"), "tenant-a", "role-doctor", map[string]any{
		"expectedRevision": 0,
		"permissions": []map[string]any{
			{"resourceKey": "patient_record", "actionKey": "read", "enabled": true, "validFrom": "2026-01-01T00:00:00Z"},
		},
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeResponse(t, rec)
	if body["revision"] != float64(1) {
		t.Errorf("revision = %v, want 1", body["revision"])
	}
}

// A stale expected revision must be rejected with 409, and the fake store's
// own precondition check proves nothing was written for it either.
func TestSaveRejectsAStaleExpectedRevisionWith409(t *testing.T) {
	store := newFakeStore()
	store.revisions["tenant-a"] = 3
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := request(t, adminOf("tenant-a"), "tenant-a", "role-doctor", map[string]any{
		"expectedRevision": 0,
		"permissions": []map[string]any{
			{"resourceKey": "patient_record", "actionKey": "read", "enabled": true, "validFrom": "2026-01-01T00:00:00Z"},
		},
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

// A resource/action not in the active catalog must be rejected before any
// write reaches the store.
func TestSaveRejectsAResourceActionNotInTheActiveCatalog(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog() // empty: nothing is known
	handler := newHandler(store, catalog)

	req := request(t, adminOf("tenant-a"), "tenant-a", "role-doctor", map[string]any{
		"expectedRevision": 0,
		"permissions": []map[string]any{
			{"resourceKey": "patient_record", "actionKey": "read", "enabled": true, "validFrom": "2026-01-01T00:00:00Z"},
		},
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if len(store.revisions) != 0 {
		t.Error("the store's revision was touched despite the catalog rejecting the write")
	}
}

// An administrator whose token scopes them to a different tenant must be
// rejected before any write, and before the catalog is even consulted.
func TestSaveRejectsAnAdministratorWithNoAuthorityOverTheTargetTenant(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := request(t, adminOf("tenant-b"), "tenant-a", "role-doctor", map[string]any{
		"expectedRevision": 0,
		"permissions": []map[string]any{
			{"resourceKey": "patient_record", "actionKey": "read", "enabled": true, "validFrom": "2026-01-01T00:00:00Z"},
		},
	})
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if len(store.revisions) != 0 {
		t.Error("the store's revision was touched despite the authority check rejecting the write")
	}
}

// A request with no verified identity in its context - meaning it reached
// this handler without the tokenauth middleware, which is a wiring defect -
// must not silently proceed as some anonymous administrator.
func TestSaveRejectsARequestWithNoVerifiedIdentity(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	raw, _ := json.Marshal(map[string]any{"expectedRevision": 0, "permissions": []map[string]any{
		{"resourceKey": "patient_record", "actionKey": "read", "enabled": true, "validFrom": "2026-01-01T00:00:00Z"},
	}})
	req := httptest.NewRequest(http.MethodPut, "/admin/authz/tenants/tenant-a/roles/role-doctor/permissions", bytes.NewReader(raw))
	req.SetPathValue("tenant", "tenant-a")
	req.SetPathValue("role", "role-doctor")

	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

// The audit event a successful save writes must record the before and after
// state of the touched permission, and the correlation id supplied on the
// request.
func TestSaveRecordsBeforeAndAfterStateAndTheCorrelationIdOnTheAuditEvent(t *testing.T) {
	store := newFakeStore()
	store.permissions[permissionKeyString(assignmentstore.RolePermissionKey{
		TenantID: "tenant-a", RoleExternalID: "role-doctor",
		ResourceKey: "patient_record", ActionKey: "read",
	})] = assignmentstore.RolePermission{Enabled: false, Revision: 0}
	catalog := newFakeCatalog("patient_record:read")
	handler := newHandler(store, catalog)

	req := request(t, adminOf("tenant-a"), "tenant-a", "role-doctor", map[string]any{
		"expectedRevision": 0,
		"permissions": []map[string]any{
			{"resourceKey": "patient_record", "actionKey": "read", "enabled": true, "validFrom": "2026-01-01T00:00:00Z"},
		},
	})
	req.Header.Set("X-Correlation-Id", "corr-xyz")
	rec := httptest.NewRecorder()
	handler.Save(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	audit := store.lastWrite.Audit
	if audit.ActorID != "admin-1" {
		t.Errorf("actorID = %q, want admin-1", audit.ActorID)
	}
	if audit.Operation != "ROLE_MATRIX_SAVE" {
		t.Errorf("operation = %q, want ROLE_MATRIX_SAVE", audit.Operation)
	}
	if audit.CorrelationID != "corr-xyz" {
		t.Errorf("correlationID = %q, want corr-xyz", audit.CorrelationID)
	}
	if audit.BeforeJSON != `{"patient_record:read":false}` {
		t.Errorf("beforeJSON = %s, want the pre-write state", audit.BeforeJSON)
	}
	if audit.AfterJSON != `{"patient_record:read":true}` {
		t.Errorf("afterJSON = %s, want the post-write state", audit.AfterJSON)
	}
}

// The outbox payload a successful save appends must carry one
// PermissionChanged event per touched permission, in the §10.2 shape a
// consumer can invalidate from directly.
func TestSaveAppendsOnePermissionChangedEventPerTouchedPermission(t *testing.T) {
	store := newFakeStore()
	catalog := newFakeCatalog("patient_record:read", "patient_record:update")
	handler := newHandler(store, catalog)

	req := request(t, adminOf("tenant-a"), "tenant-a", "role-doctor", map[string]any{
		"expectedRevision": 0,
		"permissions": []map[string]any{
			{"resourceKey": "patient_record", "actionKey": "read", "enabled": true, "validFrom": "2026-01-01T00:00:00Z"},
			{"resourceKey": "patient_record", "actionKey": "update", "enabled": false, "validFrom": "2026-01-01T00:00:00Z"},
		},
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
	if len(events) != 2 {
		t.Fatalf("got %d events, want one per touched permission", len(events))
	}

	byAction := make(map[string]permissionevents.PermissionChanged, len(events))
	for _, event := range events {
		byAction[event.Action] = event
	}

	read, ok := byAction["read"]
	if !ok {
		t.Fatal("no PermissionChanged event for the read permission")
	}
	if read.EventType != permissionevents.EventTypePermissionChanged {
		t.Errorf("eventType = %q, want %q", read.EventType, permissionevents.EventTypePermissionChanged)
	}
	if read.TenantID != "tenant-a" || read.SubjectType != permissionevents.SubjectRole || read.SubjectID != "role-doctor" {
		t.Errorf("read event = %+v, want tenant-a/ROLE/role-doctor", read)
	}
	if read.Resource != "patient_record" || !read.Enabled {
		t.Errorf("read event = %+v, want patient_record enabled=true", read)
	}
	if read.Revision != 1 {
		t.Errorf("read event revision = %d, want 1", read.Revision)
	}

	update, ok := byAction["update"]
	if !ok {
		t.Fatal("no PermissionChanged event for the update permission")
	}
	if update.Enabled {
		t.Error("the update event reads enabled=true, want false (it was revoked)")
	}
}

// Read must return every permission row a role carries, exactly as
// stored, alongside the tenant's current revision (§9.4's role matrix
// screen).
func TestReadReturnsEveryPermissionRowAndTheCurrentRevision(t *testing.T) {
	store := newFakeStore()
	store.permissions[permissionKeyString(assignmentstore.RolePermissionKey{
		TenantID: "tenant-a", RoleExternalID: "role-doctor", ResourceKey: "patient_record", ActionKey: "read",
	})] = assignmentstore.RolePermission{
		Key: assignmentstore.RolePermissionKey{
			TenantID: "tenant-a", RoleExternalID: "role-doctor", ResourceKey: "patient_record", ActionKey: "read",
		},
		Enabled: true, ValidFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Revision: 3,
	}
	store.permissions[permissionKeyString(assignmentstore.RolePermissionKey{
		TenantID: "tenant-a", RoleExternalID: "role-nurse", ResourceKey: "patient_record", ActionKey: "read",
	})] = assignmentstore.RolePermission{
		Key: assignmentstore.RolePermissionKey{
			TenantID: "tenant-a", RoleExternalID: "role-nurse", ResourceKey: "patient_record", ActionKey: "read",
		},
		Enabled: true, Revision: 1,
	}
	store.revisions["tenant-a"] = 3
	handler := newHandler(store, newFakeCatalog())

	req := httptest.NewRequest(http.MethodGet, "/admin/authz/tenants/tenant-a/roles/role-doctor/permissions", nil)
	req.SetPathValue("tenant", "tenant-a")
	req.SetPathValue("role", "role-doctor")
	req = req.WithContext(tokenauth.WithIdentity(req.Context(), adminOf("tenant-a")))

	rec := httptest.NewRecorder()
	handler.Read(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeResponse(t, rec)
	if body["revision"] != float64(3) {
		t.Errorf("revision = %v, want 3", body["revision"])
	}
	permissions, ok := body["permissions"].([]any)
	if !ok || len(permissions) != 1 {
		t.Fatalf("permissions = %v, want exactly role-doctor's one row", body["permissions"])
	}
	row := permissions[0].(map[string]any)
	if row["resourceKey"] != "patient_record" || row["actionKey"] != "read" {
		t.Errorf("row = %v, want patient_record/read", row)
	}
}

func TestReadRejectsAnAdministratorFromAnotherTenant(t *testing.T) {
	store := newFakeStore()
	handler := newHandler(store, newFakeCatalog())

	req := httptest.NewRequest(http.MethodGet, "/admin/authz/tenants/tenant-a/roles/role-doctor/permissions", nil)
	req.SetPathValue("tenant", "tenant-a")
	req.SetPathValue("role", "role-doctor")
	req = req.WithContext(tokenauth.WithIdentity(req.Context(), adminOf("tenant-b")))

	rec := httptest.NewRecorder()
	handler.Read(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

func TestCurrentRevisionReportsZeroForANeverSavedTenant(t *testing.T) {
	store := newFakeStore()
	handler := newHandler(store, newFakeCatalog())

	req := httptest.NewRequest(http.MethodGet, "/admin/authz/tenants/tenant-a/permission-revision", nil)
	req.SetPathValue("tenant", "tenant-a")
	req = req.WithContext(tokenauth.WithIdentity(req.Context(), adminOf("tenant-a")))

	rec := httptest.NewRecorder()
	handler.CurrentRevision(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeResponse(t, rec)
	if body["revision"] != float64(0) {
		t.Errorf("revision = %v, want 0", body["revision"])
	}
}
