package tenantonboarding_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tenantonboarding"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

func readableSecretFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("a-service-account-secret"), 0o600); err != nil {
		t.Fatalf("writing a fixture secret file: %v", err)
	}
	return path
}

func request(t *testing.T, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling the request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/authz/tenants", bytes.NewReader(raw))
	return req.WithContext(tokenauth.WithIdentity(req.Context(), tokenauth.Identity{
		PrincipalID: "user-admin",
		TenantID:    "tenant-a",
		HospitalID:  "hospital-1",
		Roles:       []string{"kc:tenant-a:realm:administrator"},
	}))
}

func TestOnboardingATenantSavesItAndAuditsTheWrite(t *testing.T) {
	secret := readableSecretFile(t)
	store := &recordingStore{}
	handler := &tenantonboarding.Handler{Store: store}

	rec := httptest.NewRecorder()
	handler.Onboard(rec, request(t, map[string]string{
		"realm":               "tenant-c",
		"issuer":              "http://localhost:8081/realms/tenant-c",
		"browserClientId":     "patient-app",
		"serviceClientId":     "authorization-admin-service",
		"credentialSecretRef": secret,
	}))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body)
	}
	if store.saved.Realm != "tenant-c" {
		t.Fatalf("saved tenant = %+v, want realm tenant-c", store.saved)
	}
	if store.saved.Issuer != "http://localhost:8081/realms/tenant-c" {
		t.Errorf("saved issuer = %q, want the request's issuer", store.saved.Issuer)
	}

	if len(store.audited) != 1 {
		t.Fatalf("audited %d events, want exactly 1", len(store.audited))
	}
	event := store.audited[0]
	if event.Operation != "TENANT_ONBOARD" {
		t.Errorf("Operation = %q, want TENANT_ONBOARD", event.Operation)
	}
	if event.ActorID != "user-admin" {
		t.Errorf("ActorID = %q, want the caller's own principal id", event.ActorID)
	}
	if event.TenantID != "tenant-c" {
		t.Errorf("TenantID = %q, want the onboarded realm", event.TenantID)
	}
}

func TestOnboardingRejectsTheSameRulesTheRegistryFileIsValidatedAgainst(t *testing.T) {
	store := &recordingStore{}
	handler := &tenantonboarding.Handler{Store: store}

	cases := map[string]map[string]string{
		"no realm": {
			"issuer": "http://localhost:8081/realms/tenant-c", "browserClientId": "patient-app",
			"credentialSecretRef": readableSecretFile(t),
		},
		"no issuer": {
			"realm": "tenant-c", "browserClientId": "patient-app",
			"credentialSecretRef": readableSecretFile(t),
		},
		"no browser client id": {
			"realm": "tenant-c", "issuer": "http://localhost:8081/realms/tenant-c",
			"credentialSecretRef": readableSecretFile(t),
		},
		"an unreadable credential secret ref": {
			"realm": "tenant-c", "issuer": "http://localhost:8081/realms/tenant-c",
			"browserClientId": "patient-app", "credentialSecretRef": "/nonexistent/path",
		},
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.Onboard(rec, request(t, body))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body)
			}
			if store.saveCalls != 0 {
				t.Error("an invalid entry reached SaveTenant")
			}
		})
	}
}

// issue #86: onboarding a duplicate realm is rejected with a clear error.
func TestOnboardingADuplicateRealmIsRejected(t *testing.T) {
	secret := readableSecretFile(t)
	store := &recordingStore{
		existing: map[string]assignmentstore.Tenant{"tenant-a": {Realm: "tenant-a"}},
	}
	handler := &tenantonboarding.Handler{Store: store}

	rec := httptest.NewRecorder()
	handler.Onboard(rec, request(t, map[string]string{
		"realm": "tenant-a", "issuer": "http://localhost:8081/realms/tenant-a",
		"browserClientId": "patient-app", "credentialSecretRef": secret,
	}))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusConflict, rec.Body)
	}
	if store.saveCalls != 0 {
		t.Error("a duplicate realm reached SaveTenant")
	}
}

func TestOnboardingRequiresAuthentication(t *testing.T) {
	store := &recordingStore{}
	handler := &tenantonboarding.Handler{Store: store}

	req := httptest.NewRequest(http.MethodPost, "/admin/authz/tenants", bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()
	handler.Onboard(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if store.saveCalls != 0 {
		t.Error("an unauthenticated request reached SaveTenant")
	}
}

func TestOnboardingRejectsAMalformedBody(t *testing.T) {
	store := &recordingStore{}
	handler := &tenantonboarding.Handler{Store: store}

	req := httptest.NewRequest(http.MethodPost, "/admin/authz/tenants", bytes.NewReader([]byte("not json")))
	req = req.WithContext(tokenauth.WithIdentity(req.Context(), tokenauth.Identity{PrincipalID: "user-admin", TenantID: "tenant-a"}))
	rec := httptest.NewRecorder()
	handler.Onboard(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

type recordingStore struct {
	existing  map[string]assignmentstore.Tenant
	saved     assignmentstore.Tenant
	saveCalls int
	audited   []assignmentstore.AuditEvent
}

func (s *recordingStore) Tenant(_ context.Context, realm string) (assignmentstore.Tenant, bool, error) {
	tenant, ok := s.existing[realm]
	return tenant, ok, nil
}

func (s *recordingStore) SaveTenant(_ context.Context, tenant assignmentstore.Tenant) error {
	s.saveCalls++
	s.saved = tenant
	return nil
}

func (s *recordingStore) AppendAuditEvent(_ context.Context, event assignmentstore.AuditEvent) error {
	s.audited = append(s.audited, event)
	return nil
}
