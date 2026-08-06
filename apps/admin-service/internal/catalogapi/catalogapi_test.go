package catalogapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/catalogapi"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

func TestReadRequiresAVerifiedIdentity(t *testing.T) {
	handler := &catalogapi.Handler{}
	rec := httptest.NewRecorder()
	handler.Read(rec, httptest.NewRequest(http.MethodGet, "/admin/authz/resources", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestReadReturnsTheCatalogAndRootPolicyRevision(t *testing.T) {
	handler := &catalogapi.Handler{
		Resources: []capabilitycatalog.ResourceEntry{
			{
				ResourceKey: "patient_record", DisplayName: "Patient record", Domain: "clinical",
				Actions: []capabilitycatalog.ActionEntry{
					{Key: "read", DisplayName: "View patient", Context: "INSTANCE"},
				},
			},
		},
		RootPolicyRevision: "root-v1.4.0",
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/authz/resources", nil)
	req = req.WithContext(tokenauth.WithIdentity(req.Context(), tokenauth.Identity{PrincipalID: "admin-1"}))
	rec := httptest.NewRecorder()
	handler.Read(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"resourceKey":"patient_record"`) {
		t.Errorf("body = %s, want patient_record", rec.Body.String())
	}
	if !contains(rec.Body.String(), `"rootPolicyRevision":"root-v1.4.0"`) {
		t.Errorf("body = %s, want the root policy revision", rec.Body.String())
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
