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

func TestCapabilityImpactRequiresAVerifiedIdentity(t *testing.T) {
	handler := &catalogapi.Handler{}
	req := httptest.NewRequest(http.MethodGet, "/admin/authz/resources/patient_record/actions/read/capabilities", nil)
	req.SetPathValue("resource", "patient_record")
	req.SetPathValue("action", "read")
	rec := httptest.NewRecorder()
	handler.CapabilityImpact(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// Issue #18's "Selecting a resource-action lists every capability that
// depends on it".
func TestCapabilityImpactListsEveryDependentCapability(t *testing.T) {
	handler := &catalogapi.Handler{
		Capabilities: []capabilitycatalog.UiCapabilityDefinition{
			{
				Key: "patient.route.edit", Module: "clinical", Context: "INSTANCE",
				Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
					{Permission: &capabilitycatalog.PermissionRequirement{Resource: "patient_record", Action: "update", TargetRef: "patient"}},
				}},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/authz/resources/patient_record/actions/update/capabilities", nil)
	req.SetPathValue("resource", "patient_record")
	req.SetPathValue("action", "update")
	req = req.WithContext(tokenauth.WithIdentity(req.Context(), tokenauth.Identity{PrincipalID: "admin-1"}))
	rec := httptest.NewRecorder()
	handler.CapabilityImpact(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"key":"patient.route.edit"`) {
		t.Errorf("body = %s, want patient.route.edit", rec.Body.String())
	}
}

// Issue #18's "A resource-action used by no capability is clearly shown
// as such rather than as an empty error" - the response must still be
// 200 with an empty list, never a 404.
func TestCapabilityImpactReturnsAnEmptyListRatherThanAnErrorWhenNothingDepends(t *testing.T) {
	handler := &catalogapi.Handler{
		Capabilities: []capabilitycatalog.UiCapabilityDefinition{
			{
				Key: "patient.route.view", Module: "clinical", Context: "INSTANCE",
				Expression: capabilitycatalog.Expression{
					Permission: &capabilitycatalog.PermissionRequirement{Resource: "patient_record", Action: "read", TargetRef: "patient"},
				},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/authz/resources/patient_record/actions/delete/capabilities", nil)
	req.SetPathValue("resource", "patient_record")
	req.SetPathValue("action", "delete")
	req = req.WithContext(tokenauth.WithIdentity(req.Context(), tokenauth.Identity{PrincipalID: "admin-1"}))
	rec := httptest.NewRecorder()
	handler.CapabilityImpact(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"capabilities":[]`) {
		t.Errorf("body = %s, want an empty capabilities array, not an error", rec.Body.String())
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
