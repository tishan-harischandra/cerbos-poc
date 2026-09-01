package tenantregistry_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
)

func TestEnvJSRendersTheRequestingHostsOwnTenant(t *testing.T) {
	resolver := tenantregistry.NewHostResolver(entries())
	handler := tenantregistry.EnvJSHandler(resolver)

	req := httptest.NewRequest(http.MethodGet, "http://tenant-b.example.test/assets/env.js", nil)
	req.Host = "tenant-b.example.test"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"oidcIssuer":"http://localhost:8081/realms/tenant-b"`) {
		t.Errorf("body does not name tenant-b's own issuer: %s", body)
	}
	if strings.Contains(body, "tenant-a") {
		t.Errorf("body named the wrong tenant: %s", body)
	}
}

func TestEnvJSRendersADifferentTenantForADifferentHost(t *testing.T) {
	resolver := tenantregistry.NewHostResolver(entries())
	handler := tenantregistry.EnvJSHandler(resolver)

	req := httptest.NewRequest(http.MethodGet, "http://tenant-a.example.test/assets/env.js", nil)
	req.Host = "tenant-a.example.test"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), `"oidcIssuer":"http://localhost:8081/realms/tenant-a"`) {
		t.Errorf("body does not name tenant-a's own issuer: %s", rec.Body.String())
	}
}

func TestEnvJSRefusesAnUnknownTenantWithAClearError(t *testing.T) {
	resolver := tenantregistry.NewHostResolver(entries())
	handler := tenantregistry.EnvJSHandler(resolver)

	req := httptest.NewRequest(http.MethodGet, "http://tenant-nonexistent.example.test/assets/env.js", nil)
	req.Host = "tenant-nonexistent.example.test"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	// Never a JavaScript blob that would look like a tenant's real
	// configuration to whatever executed it.
	if strings.Contains(rec.Body.String(), "window.__ENV__") {
		t.Errorf("an unknown tenant still produced a JavaScript environment blob: %s", rec.Body.String())
	}
	if rec.Result().Header.Get("Content-Type") == "text/javascript; charset=utf-8" {
		t.Error("an unknown tenant's error response claimed to be JavaScript")
	}
}

func TestEnvJSIsNeverCached(t *testing.T) {
	resolver := tenantregistry.NewHostResolver(entries())
	handler := tenantregistry.EnvJSHandler(resolver)

	req := httptest.NewRequest(http.MethodGet, "http://tenant-a.example.test/assets/env.js", nil)
	req.Host = "tenant-a.example.test"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
}
