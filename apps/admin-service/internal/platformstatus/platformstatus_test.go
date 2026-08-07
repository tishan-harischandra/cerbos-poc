package platformstatus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/adsclient"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/platformstatus"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

func adminOf(tenant string) tokenauth.Identity {
	return tokenauth.Identity{PrincipalID: "admin-1", TenantID: tenant, RawToken: "admin-token"}
}

func get(target string, identity tokenauth.Identity) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	return req.WithContext(tokenauth.WithIdentity(req.Context(), identity))
}

func buildArchiveAt(t *testing.T, dir, revision, commit string) policyrelease.Archive {
	t.Helper()
	src := t.TempDir()
	path := filepath.Join(src, "resources", "patient_record.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("kind: ResourcePolicy"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	archive, err := policyrelease.BuildArchive(context.Background(), policyrelease.ArchiveInput{
		SourceDir: src, Revision: revision, Commit: commit, OutputDir: dir,
	})
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}
	return archive
}

func TestPolicyReleases_ReportsTheCurrentRevisionAndHistory(t *testing.T) {
	dir := t.TempDir()
	store := policyrelease.NewStore(dir)
	archive := buildArchiveAt(t, dir, "root-v1.4.0", "bbb")
	if err := store.MarkActive(archive); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}
	if err := store.RecordAttempt(policyrelease.HistoryEntry{
		Revision: "root-v1.4.0", Commit: "bbb", Activated: true,
	}); err != nil {
		t.Fatalf("RecordAttempt: %v", err)
	}
	if err := store.RecordAttempt(policyrelease.HistoryEntry{
		Revision: "root-v1.5.0", Commit: "ccc", Activated: false, Error: "replica cerbos-b failed to reload",
	}); err != nil {
		t.Fatalf("RecordAttempt: %v", err)
	}

	handler := &platformstatus.Handler{PolicyStore: store}
	rec := httptest.NewRecorder()
	handler.PolicyReleases(rec, get("/admin/authz/policy-releases", adminOf("tenant-a")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	var body struct {
		Current struct {
			Revision string `json:"revision"`
			Commit   string `json:"commit"`
		} `json:"current"`
		History []struct {
			Revision  string `json:"revision"`
			Activated bool   `json:"activated"`
			Error     string `json:"error"`
		} `json:"history"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.Current.Revision != "root-v1.4.0" || body.Current.Commit != "bbb" {
		t.Fatalf("current = %+v, want root-v1.4.0/bbb", body.Current)
	}
	if len(body.History) != 2 {
		t.Fatalf("len(history) = %d, want 2", len(body.History))
	}
	if !body.History[0].Activated {
		t.Errorf("history[0].Activated = false, want true")
	}
	if body.History[1].Activated || body.History[1].Error == "" {
		t.Errorf("history[1] = %+v, want a failed attempt with an error", body.History[1])
	}
}

func TestPolicyReleases_ReportsNoCurrentRevisionWhenNothingHasEverActivated(t *testing.T) {
	dir := t.TempDir()
	handler := &platformstatus.Handler{PolicyStore: policyrelease.NewStore(dir)}

	rec := httptest.NewRecorder()
	handler.PolicyReleases(rec, get("/admin/authz/policy-releases", adminOf("tenant-a")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	var body struct {
		Current *struct {
			Revision string `json:"revision"`
		} `json:"current"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.Current != nil {
		t.Fatalf("current = %+v, want nil", body.Current)
	}
}

type fakeADS struct {
	convergenceResp adsclient.ConvergenceResponse
	convergenceErr  error
	directoryErr    error
	lastToken       string
}

func (f *fakeADS) Convergence(_ context.Context, token string) (adsclient.ConvergenceResponse, error) {
	f.lastToken = token
	return f.convergenceResp, f.convergenceErr
}

func (f *fakeADS) DirectoryHealth(_ context.Context, token string) error {
	f.lastToken = token
	return f.directoryErr
}

func TestConvergence_ProxiesTheADSsOwnConvergenceReportForTheCallersTenant(t *testing.T) {
	ads := &fakeADS{convergenceResp: adsclient.ConvergenceResponse{
		Tenant: "tenant-a", CachedRevision: 4, ActualRevision: 4, Converged: true,
	}}
	handler := &platformstatus.Handler{ADS: ads}

	req := get("/admin/authz/tenants/tenant-a/convergence", adminOf("tenant-a"))
	req.SetPathValue("tenant", "tenant-a")
	rec := httptest.NewRecorder()
	handler.Convergence(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	if ads.lastToken != "admin-token" {
		t.Errorf("token forwarded = %q, want the caller's own token", ads.lastToken)
	}
	var body adsclient.ConvergenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if !body.Converged {
		t.Fatalf("body = %+v, want converged", body)
	}
}

func TestConvergence_RejectsAnAdministratorFromAnotherTenant(t *testing.T) {
	handler := &platformstatus.Handler{ADS: &fakeADS{}}

	req := get("/admin/authz/tenants/tenant-a/convergence", adminOf("tenant-b"))
	req.SetPathValue("tenant", "tenant-a")
	rec := httptest.NewRecorder()
	handler.Convergence(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body)
	}
}

func TestIdPDiagnostics_ReportsTheSelectedProviderAndOKConnectivity(t *testing.T) {
	handler := &platformstatus.Handler{
		ADS:                  &fakeADS{},
		IdPType:              "KEYCLOAK",
		IdPRoleSource:        "CLIENT",
		IdPTenantMappingMode: "CLAIM",
	}

	rec := httptest.NewRecorder()
	handler.IdPDiagnostics(rec, get("/admin/idp/diagnostics", adminOf("tenant-a")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	var body struct {
		Provider          string `json:"provider"`
		RoleSource        string `json:"roleSource"`
		TenantMappingMode string `json:"tenantMappingMode"`
		Connectivity      string `json:"connectivity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.Provider != "KEYCLOAK" || body.Connectivity != "ok" {
		t.Fatalf("body = %+v, want provider KEYCLOAK, connectivity ok", body)
	}
}

func TestIdPDiagnostics_ReportsDegradedWhenTheIdPAdminAPIIsUnreachable(t *testing.T) {
	handler := &platformstatus.Handler{
		ADS:     &fakeADS{directoryErr: context.DeadlineExceeded},
		IdPType: "KEYCLOAK",
	}

	rec := httptest.NewRecorder()
	handler.IdPDiagnostics(rec, get("/admin/idp/diagnostics", adminOf("tenant-a")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	var body struct {
		Connectivity string `json:"connectivity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.Connectivity != "degraded" {
		t.Fatalf("connectivity = %q, want degraded", body.Connectivity)
	}
}
