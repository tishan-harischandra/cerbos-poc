package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/rolematrix"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/server"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/useroverride"
	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

type fakeVerifier struct{}

func (fakeVerifier) Verify(context.Context, string) (tokenverifier.VerifiedToken, error) {
	return tokenverifier.VerifiedToken{}, nil
}

func TestHealthzAlwaysAnswers(t *testing.T) {
	handler := server.New(server.Config{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRoleMatrixRoutesAreAbsentWithoutARoleMatrixHandler(t *testing.T) {
	handler := server.New(server.Config{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/admin/authz/tenants/tenant-a/permission-revision", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no role-matrix handler is configured", rec.Code)
	}
}

func TestRoleMatrixRoutesRequireABearerToken(t *testing.T) {
	handler := server.New(server.Config{
		Verifier:   fakeVerifier{},
		RoleMatrix: &rolematrix.Handler{},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/admin/authz/tenants/tenant-a/permission-revision", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 with no bearer token", rec.Code)
	}
}

func TestUserOverrideRoutesAreAbsentWithoutAUserOverrideHandler(t *testing.T) {
	handler := server.New(server.Config{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/admin/authz/tenants/tenant-a/hospitals/hospital-1/users/user-1/overrides?resource=patient_record", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no user-override handler is configured", rec.Code)
	}
}

func TestUserOverrideRoutesRequireABearerToken(t *testing.T) {
	handler := server.New(server.Config{
		Verifier:     fakeVerifier{},
		UserOverride: &useroverride.Handler{},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/admin/authz/tenants/tenant-a/hospitals/hospital-1/users/user-1/overrides?resource=patient_record", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 with no bearer token", rec.Code)
	}
}

func TestReadyzReportsReadyWithNoDependencies(t *testing.T) {
	handler := server.New(server.Config{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
