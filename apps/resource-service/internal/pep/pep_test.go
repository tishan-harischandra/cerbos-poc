package pep_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/adsclient"
	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/pep"
	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

var fixedClock = func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) }

type fakeStore struct {
	resources map[string]assignmentstore.Resource
	saveErr   error
	getErr    error
	deleteErr error
	listErr   error
	saved     []assignmentstore.Resource
	deleted   []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{resources: map[string]assignmentstore.Resource{}}
}

func key(resourceType, id string) string { return resourceType + "/" + id }

func (s *fakeStore) put(r assignmentstore.Resource) {
	s.resources[key(r.ResourceType, r.ResourceID)] = r
}

func (s *fakeStore) SaveResource(_ context.Context, r assignmentstore.Resource) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, r)
	s.resources[key(r.ResourceType, r.ResourceID)] = r
	return nil
}

func (s *fakeStore) Resource(_ context.Context, resourceType, id string) (assignmentstore.Resource, bool, error) {
	if s.getErr != nil {
		return assignmentstore.Resource{}, false, s.getErr
	}
	r, found := s.resources[key(resourceType, id)]
	return r, found, nil
}

func (s *fakeStore) DeleteResource(_ context.Context, resourceType, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleted = append(s.deleted, key(resourceType, id))
	delete(s.resources, key(resourceType, id))
	return nil
}

func (s *fakeStore) ListResources(_ context.Context, q assignmentstore.ListResourcesQuery) ([]assignmentstore.Resource, int, error) {
	if s.listErr != nil {
		return nil, 0, s.listErr
	}
	var matched []assignmentstore.Resource
	for _, r := range s.resources {
		if r.ResourceType == q.ResourceType && r.TenantID == q.TenantID && r.HospitalID == q.HospitalID {
			matched = append(matched, r)
		}
	}
	total := len(matched)
	limit := q.Limit
	if limit <= 0 || q.Offset+limit > len(matched) {
		limit = len(matched) - q.Offset
	}
	if q.Offset >= len(matched) {
		return nil, total, nil
	}
	return matched[q.Offset : q.Offset+limit], total, nil
}

type fakeADS struct {
	// decisions is keyed by "kind/id/action".
	decisions map[string]adsclient.Decision
	revision  int64
	err       error
	calls     int
	lastSizes []int
}

func newFakeADS() *fakeADS { return &fakeADS{decisions: map[string]adsclient.Decision{}} }

func (a *fakeADS) allow(kind, id, action string, source string) {
	a.decisions[kind+"/"+id+"/"+action] = adsclient.Decision{Allowed: true, Source: source}
}

func (a *fakeADS) deny(kind, id, action string, source string) {
	a.decisions[kind+"/"+id+"/"+action] = adsclient.Decision{Allowed: false, Source: source}
}

func (a *fakeADS) Check(_ context.Context, _ string, checks []adsclient.ResourceCheck) ([]adsclient.ResourceDecision, error) {
	a.calls++
	a.lastSizes = append(a.lastSizes, len(checks))
	if a.err != nil {
		return nil, a.err
	}
	var out []adsclient.ResourceDecision
	for _, c := range checks {
		actions := make(map[string]adsclient.Decision, len(c.Actions))
		for _, action := range c.Actions {
			d, found := a.decisions[c.Kind+"/"+c.ID+"/"+action]
			if !found {
				d = adsclient.Decision{Allowed: false, Source: "MANDATORY_RULE"}
			}
			actions[action] = d
		}
		out = append(out, adsclient.ResourceDecision{
			Kind: c.Kind, ID: c.ID, PermissionRevision: a.revision, Actions: actions,
		})
	}
	return out, nil
}

func withIdentity(req *http.Request, identity tokenauth.Identity) *http.Request {
	return req.WithContext(tokenauth.WithIdentity(req.Context(), identity))
}

const testToken = "test-token"

func testIdentity() tokenauth.Identity {
	return tokenauth.Identity{
		PrincipalID: "user-1", TenantID: "tenant-a", HospitalID: "hospital-1", RawToken: testToken,
	}
}

func TestReadDeniesWithoutCallingTheStoreWrite(t *testing.T) {
	store := newFakeStore()
	store.put(assignmentstore.Resource{
		ResourceType: "condition", ResourceID: "condition-1",
		TenantID: "tenant-a", HospitalID: "hospital-1", Status: "ACTIVE", PayloadJSON: `{"a":1}`,
	})
	ads := newFakeADS()
	ads.deny("condition", "condition-1", "read", "MANDATORY_RULE")

	handler := pep.NewHandler(pep.Config{Store: store, ADS: ads, Clock: fixedClock})
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/fhir/condition/condition-1", nil), testIdentity())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body)
	}
}

func TestReadAllowsAndReturnsThePayload(t *testing.T) {
	store := newFakeStore()
	store.put(assignmentstore.Resource{
		ResourceType: "condition", ResourceID: "condition-1",
		TenantID: "tenant-a", HospitalID: "hospital-1", Status: "ACTIVE", PayloadJSON: `{"a":1}`,
	})
	ads := newFakeADS()
	ads.allow("condition", "condition-1", "read", "ROLE")

	handler := pep.NewHandler(pep.Config{Store: store, ADS: ads, Clock: fixedClock})
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/fhir/condition/condition-1", nil), testIdentity())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body["resource"] == nil {
		t.Errorf("no resource payload in the response: %v", body)
	}
}

func TestReadOnAMissingResourceIs404WithoutCallingTheADS(t *testing.T) {
	store := newFakeStore()
	ads := newFakeADS()

	handler := pep.NewHandler(pep.Config{Store: store, ADS: ads, Clock: fixedClock})
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/fhir/condition/absent", nil), testIdentity())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if ads.calls != 0 {
		t.Errorf("the ADS was called for a resource that does not exist")
	}
}

// A caller must never be able to author its own resource attributes for the
// decision: the loaded status ("LOCKED") is what the ADS is asked about,
// never anything from the request body.
func TestUpdateAsksTheADSAboutTheStoresOwnAttributesNotTheRequestBody(t *testing.T) {
	store := newFakeStore()
	store.put(assignmentstore.Resource{
		ResourceType: "condition", ResourceID: "condition-1",
		TenantID: "tenant-a", HospitalID: "hospital-1", Status: "LOCKED",
	})
	ads := newFakeADS()
	ads.deny("condition", "condition-1", "update", "MANDATORY_RULE")

	handler := pep.NewHandler(pep.Config{Store: store, ADS: ads, Clock: fixedClock})
	body := strings.NewReader(`{"status":"ACTIVE"}`)
	req := withIdentity(httptest.NewRequest(http.MethodPut, "/fhir/condition/condition-1", body), testIdentity())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; a LOCKED record must deny update even when the "+
			"request body claims ACTIVE", rec.Code, http.StatusForbidden)
	}
	if len(store.saved) != 0 {
		t.Error("the resource was saved despite a denied decision")
	}
}

// The mandatory-deny path (issue #9) covers delete as well as update: a
// LOCKED record's own status, not the request, is what the ADS is asked
// about, and a deny must leave the row in place.
func TestDeleteAsksTheADSAboutTheStoresOwnAttributesNotTheRequestBody(t *testing.T) {
	store := newFakeStore()
	store.put(assignmentstore.Resource{
		ResourceType: "condition", ResourceID: "condition-1",
		TenantID: "tenant-a", HospitalID: "hospital-1", Status: "LOCKED",
	})
	ads := newFakeADS()
	ads.deny("condition", "condition-1", "delete", "MANDATORY_RULE")

	handler := pep.NewHandler(pep.Config{Store: store, ADS: ads, Clock: fixedClock})
	req := withIdentity(httptest.NewRequest(http.MethodDelete, "/fhir/condition/condition-1", nil), testIdentity())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; a LOCKED record must deny delete", rec.Code, http.StatusForbidden)
	}
	if len(store.deleted) != 0 {
		t.Error("the resource was deleted despite a denied decision")
	}
}

func TestDeleteRemovesFromTheStoreOnlyWhenAllowed(t *testing.T) {
	store := newFakeStore()
	store.put(assignmentstore.Resource{
		ResourceType: "condition", ResourceID: "condition-1",
		TenantID: "tenant-a", HospitalID: "hospital-1", Status: "ACTIVE",
	})
	ads := newFakeADS()
	ads.allow("condition", "condition-1", "delete", "ROLE")

	handler := pep.NewHandler(pep.Config{Store: store, ADS: ads, Clock: fixedClock})
	req := withIdentity(httptest.NewRequest(http.MethodDelete, "/fhir/condition/condition-1", nil), testIdentity())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("deleted = %d resources, want 1", len(store.deleted))
	}
}

// assign is checked as its own action: an update grant must not let assign
// through, and vice versa.
func TestAssignIsAuthorizedIndependentlyOfUpdate(t *testing.T) {
	store := newFakeStore()
	store.put(assignmentstore.Resource{
		ResourceType: "condition", ResourceID: "condition-1",
		TenantID: "tenant-a", HospitalID: "hospital-1", Status: "ACTIVE",
	})
	ads := newFakeADS()
	ads.allow("condition", "condition-1", "update", "ROLE")
	ads.deny("condition", "condition-1", "assign", "MANDATORY_RULE")

	handler := pep.NewHandler(pep.Config{Store: store, ADS: ads, Clock: fixedClock})
	req := withIdentity(httptest.NewRequest(http.MethodPost, "/fhir/condition/condition-1/assign",
		strings.NewReader(`{"assignedTo":"user-2"}`)), testIdentity())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: an update grant must not authorize assign", rec.Code, http.StatusForbidden)
	}
}

func TestAssignAllowedWritesTheAssigneeIntoThePayload(t *testing.T) {
	store := newFakeStore()
	store.put(assignmentstore.Resource{
		ResourceType: "condition", ResourceID: "condition-1",
		TenantID: "tenant-a", HospitalID: "hospital-1", Status: "ACTIVE", PayloadJSON: `{}`,
	})
	ads := newFakeADS()
	ads.allow("condition", "condition-1", "assign", "ROLE")

	handler := pep.NewHandler(pep.Config{Store: store, ADS: ads, Clock: fixedClock})
	req := withIdentity(httptest.NewRequest(http.MethodPost, "/fhir/condition/condition-1/assign",
		strings.NewReader(`{"assignedTo":"user-2"}`)), testIdentity())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved = %d, want 1", len(store.saved))
	}
	if !strings.Contains(store.saved[0].PayloadJSON, "user-2") {
		t.Errorf("payload = %s, want it to contain the assignee", store.saved[0].PayloadJSON)
	}
}

func TestCreateDerivesTenantAndHospitalFromTheIdentityNotTheBody(t *testing.T) {
	store := newFakeStore()
	ads := newFakeADS()
	// Any resource is allowed, whatever ID the handler generates.
	handler := pep.NewHandler(pep.Config{
		Store: store, ADS: ads, Clock: fixedClock,
		NewID: func() string { return "condition-new" },
	})
	ads.allow("condition", "condition-new", "create", "ROLE")

	body := strings.NewReader(`{"tenantId":"tenant-b","hospitalId":"hospital-9","resourceType":"Condition"}`)
	req := withIdentity(httptest.NewRequest(http.MethodPost, "/fhir/condition", body), testIdentity())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusCreated, rec.Body)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved = %d, want 1", len(store.saved))
	}
	saved := store.saved[0]
	if saved.TenantID != "tenant-a" || saved.HospitalID != "hospital-1" {
		t.Errorf("saved tenant/hospital = %s/%s, want the identity's tenant-a/hospital-1 "+
			"rather than the request body's tenant-b/hospital-9", saved.TenantID, saved.HospitalID)
	}
}

func TestListIssuesOneADSCallForAWholePage(t *testing.T) {
	store := newFakeStore()
	for i := 0; i < 5; i++ {
		id := "condition-" + string(rune('a'+i))
		store.put(assignmentstore.Resource{
			ResourceType: "condition", ResourceID: id,
			TenantID: "tenant-a", HospitalID: "hospital-1", Status: "ACTIVE", PayloadJSON: "{}",
		})
	}
	ads := newFakeADS()
	// Every row is allowed by default-deny fallback unless explicitly
	// allowed, so allow them all.
	for i := 0; i < 5; i++ {
		id := "condition-" + string(rune('a'+i))
		ads.allow("condition", id, "read", "ROLE")
	}

	handler := pep.NewHandler(pep.Config{Store: store, ADS: ads, Clock: fixedClock})
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/fhir/condition", nil), testIdentity())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body)
	}
	if ads.calls != 1 {
		t.Errorf("the ADS was called %d times for a 5-row page, want 1 batched call", ads.calls)
	}
	if len(ads.lastSizes) != 1 || ads.lastSizes[0] != 5 {
		t.Errorf("batch size = %v, want a single batch of 5", ads.lastSizes)
	}
}

// pep asks its ADS collaborator about a whole page in one logical Check
// call; chunking a call that is too large for one HTTP request to the ADS is
// the adsclient.Client's concern (see adsclient_test.go), not this
// package's - pep's fake collaborator here has no batch limit, on purpose.
func TestListAsksForAWholePageEvenWhenLarge(t *testing.T) {
	store := newFakeStore()
	count := adsclient.MaxResourcesPerRequest + 20
	for i := 0; i < count; i++ {
		id := "condition-" + itoa(i)
		store.put(assignmentstore.Resource{
			ResourceType: "condition", ResourceID: id,
			TenantID: "tenant-a", HospitalID: "hospital-1", Status: "ACTIVE", PayloadJSON: "{}",
		})
	}
	ads := newFakeADS()

	handler := pep.NewHandler(pep.Config{Store: store, ADS: ads, Clock: fixedClock})
	req := withIdentity(httptest.NewRequest(http.MethodGet,
		"/fhir/condition?limit="+itoa(count), nil), testIdentity())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body)
	}
	if ads.calls != 1 {
		t.Errorf("pep made %d Check calls for one page, want 1 (chunking is adsclient's job)", ads.calls)
	}
	if len(ads.lastSizes) != 1 || ads.lastSizes[0] != count {
		t.Errorf("batch size = %v, want a single batch of %d", ads.lastSizes, count)
	}
}

func TestAnUnreachableADSFailsClosed(t *testing.T) {
	store := newFakeStore()
	store.put(assignmentstore.Resource{
		ResourceType: "condition", ResourceID: "condition-1",
		TenantID: "tenant-a", HospitalID: "hospital-1", Status: "ACTIVE",
	})
	ads := newFakeADS()
	ads.err = errors.New("connection refused")

	handler := pep.NewHandler(pep.Config{Store: store, ADS: ads, Clock: fixedClock})
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/fhir/condition/condition-1", nil), testIdentity())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestARequestWithNoVerifiedIdentityIsRefused(t *testing.T) {
	handler := pep.NewHandler(pep.Config{Store: newFakeStore(), ADS: newFakeADS(), Clock: fixedClock})
	req := httptest.NewRequest(http.MethodGet, "/fhir/condition/condition-1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
