package auditsearch_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/auditsearch"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

type fakeStore struct {
	lastQuery assignmentstore.AuditEventSearchQuery
	events    []assignmentstore.AuditEvent
	total     int
	err       error
}

func (f *fakeStore) SearchAuditEvents(_ context.Context, query assignmentstore.AuditEventSearchQuery) ([]assignmentstore.AuditEvent, int, error) {
	f.lastQuery = query
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.events, f.total, nil
}

func newHandler(store *fakeStore) *auditsearch.Handler {
	return &auditsearch.Handler{Store: store}
}

func request(t *testing.T, identity tokenauth.Identity, rawQuery string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/authz/audit?"+rawQuery, nil)
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

// A search with a tenant the administrator's own token scopes them to
// forwards every documented filter to the store and reports the total
// alongside the page.
func TestSearchForwardsEveryDimensionAndReportsTheTotal(t *testing.T) {
	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		events: []assignmentstore.AuditEvent{
			{EventID: "audit-1", ActorID: "admin-2", Operation: "ROLE_MATRIX_SAVE", TenantID: "tenant-a", CreatedAt: created},
		},
		total: 5,
	}
	handler := newHandler(store)

	req := request(t, adminOf("tenant-a"),
		"tenant=tenant-a&hospital=hospital-1&actor=admin-2&role=role-doctor&user=user-1"+
			"&resource=patient_record&action=read&limit=2&offset=4")
	rec := httptest.NewRecorder()
	handler.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	want := assignmentstore.AuditEventSearchQuery{
		TenantID: "tenant-a", HospitalID: "hospital-1", ActorID: "admin-2",
		RoleExternalID: "role-doctor", TargetUserID: "user-1",
		ResourceKey: "patient_record", ActionKey: "read",
		Limit: 2, Offset: 4,
	}
	if store.lastQuery != want {
		t.Errorf("query = %+v, want %+v", store.lastQuery, want)
	}

	body := decodeResponse(t, rec)
	if body["totalCount"] != float64(5) {
		t.Errorf("totalCount = %v, want 5", body["totalCount"])
	}
	events, ok := body["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("events = %v, want exactly one", body["events"])
	}
}

// A request with no tenant at all must be refused before the store is ever
// asked: an administration query without a tenant predicate is exactly the
// mistake §8.2 warns against.
func TestSearchRejectsAMissingTenant(t *testing.T) {
	store := &fakeStore{}
	handler := newHandler(store)

	req := request(t, adminOf("tenant-a"), "actor=admin-2")
	rec := httptest.NewRecorder()
	handler.Search(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if store.lastQuery != (assignmentstore.AuditEventSearchQuery{}) {
		t.Error("the store was asked despite the missing tenant")
	}
}

// An administrator whose token scopes them to a different tenant must be
// rejected before the store is ever asked, the same guarantee every other
// administration endpoint in this service gives.
func TestSearchRejectsAnAdministratorWithNoAuthorityOverTheTargetTenant(t *testing.T) {
	store := &fakeStore{}
	handler := newHandler(store)

	req := request(t, adminOf("tenant-b"), "tenant=tenant-a")
	rec := httptest.NewRecorder()
	handler.Search(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if store.lastQuery != (assignmentstore.AuditEventSearchQuery{}) {
		t.Error("the store was asked despite the authority check rejecting the search")
	}
}

// A request with no verified identity at all is a wiring defect, not a
// caller error, but the handler must still refuse it cleanly.
func TestSearchRejectsARequestWithNoVerifiedIdentity(t *testing.T) {
	store := &fakeStore{}
	handler := newHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/admin/authz/audit?tenant=tenant-a", nil)
	rec := httptest.NewRecorder()
	handler.Search(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

// A malformed from/to timestamp is rejected with 400 rather than silently
// ignored, so an administrator's mistyped date range fails loudly.
func TestSearchRejectsAMalformedDateRange(t *testing.T) {
	store := &fakeStore{}
	handler := newHandler(store)

	req := request(t, adminOf("tenant-a"), "tenant=tenant-a&from=not-a-date")
	rec := httptest.NewRecorder()
	handler.Search(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// A store failure is reported as 500, not silently swallowed.
func TestSearchReportsAStoreFailure(t *testing.T) {
	store := &fakeStore{err: context.DeadlineExceeded}
	handler := newHandler(store)

	req := request(t, adminOf("tenant-a"), "tenant=tenant-a")
	rec := httptest.NewRecorder()
	handler.Search(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
}
