package adsclient_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/adsclient"
)

// The client forwards the caller's own bearer token, never one it mints
// itself - the ADS's own authentication middleware verifies it exactly
// as it would for any other caller.
func TestSimulateAccessForwardsTheCallersOwnBearerToken(t *testing.T) {
	var gotAuth, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(adsclient.SimulateAccessResponse{
			Allowed: true, Source: "ROLE", CerbosCallID: "call-1", PermissionRevision: 4,
		})
	}))
	defer server.Close()

	client := adsclient.New(server.URL)
	resp, err := client.SimulateAccess(t.Context(), "admin-token", adsclient.SimulateAccessRequest{
		TenantID: "tenant-a", HospitalID: "hospital-1", PrincipalID: "user-doctor",
		Resource: adsclient.SimulateTarget{Kind: "patient_record", ID: "patient-456"},
		Action:   "read",
	})
	if err != nil {
		t.Fatalf("SimulateAccess: %v", err)
	}

	if gotAuth != "Bearer admin-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer admin-token")
	}
	if gotPath != "/internal/authz/simulate" {
		t.Errorf("path = %q, want /internal/authz/simulate", gotPath)
	}
	if !resp.Allowed || resp.Source != "ROLE" {
		t.Errorf("response = %+v, want allowed=true source=ROLE", resp)
	}
}

func TestSimulateAccessReturnsAnErrorForANonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"the PDP is unreachable"}`))
	}))
	defer server.Close()

	client := adsclient.New(server.URL)
	_, err := client.SimulateAccess(t.Context(), "admin-token", adsclient.SimulateAccessRequest{})
	if err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}

func TestSimulateCapabilitiesForwardsTheCallersOwnBearerTokenAndReturnsTheRequirementTree(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(adsclient.SimulateCapabilitiesResponse{
			Capabilities: map[string]adsclient.CapabilityResult{
				"patient.route.edit": {Allowed: true},
			},
			RequirementTree: []adsclient.LeafDecision{
				{Resource: "patient_record", Action: "read", Target: "sample:patient", Allowed: true, Reason: "ROLE"},
			},
		})
	}))
	defer server.Close()

	client := adsclient.New(server.URL)
	resp, err := client.SimulateCapabilities(t.Context(), "admin-token", adsclient.SimulateCapabilitiesRequest{
		Module: "clinical", CapabilityKeys: []string{"patient.route.edit"},
		TenantID: "tenant-a", HospitalID: "hospital-1", PrincipalID: "user-doctor",
	})
	if err != nil {
		t.Fatalf("SimulateCapabilities: %v", err)
	}

	if gotPath != "/internal/capabilities/simulate" {
		t.Errorf("path = %q, want /internal/capabilities/simulate", gotPath)
	}
	if !resp.Capabilities["patient.route.edit"].Allowed {
		t.Error("patient.route.edit = denied, want allowed")
	}
	if len(resp.RequirementTree) != 1 || resp.RequirementTree[0].Action != "read" {
		t.Errorf("requirementTree = %+v, want one read leaf", resp.RequirementTree)
	}
}

func TestConvergenceForwardsTheCallersOwnBearerTokenAndReturnsTheStatus(t *testing.T) {
	var gotAuth, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(adsclient.ConvergenceResponse{
			Tenant: "tenant-a", CachedRevision: 4, ActualRevision: 4, Converged: true,
		})
	}))
	defer server.Close()

	client := adsclient.New(server.URL)
	resp, err := client.Convergence(t.Context(), "admin-token")
	if err != nil {
		t.Fatalf("Convergence: %v", err)
	}

	if gotAuth != "Bearer admin-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer admin-token")
	}
	if gotPath != "/internal/cache/convergence" {
		t.Errorf("path = %q, want /internal/cache/convergence", gotPath)
	}
	if !resp.Converged || resp.Tenant != "tenant-a" {
		t.Errorf("response = %+v, want converged tenant-a", resp)
	}
}

func TestDirectoryHealthSucceedsWhenTheADSAnswers(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "offset": 0, "limit": 1, "hasMore": false})
	}))
	defer server.Close()

	client := adsclient.New(server.URL)
	if err := client.DirectoryHealth(t.Context(), "admin-token"); err != nil {
		t.Fatalf("DirectoryHealth: %v", err)
	}
	if gotPath != "/internal/directory/roles" {
		t.Errorf("path = %q, want /internal/directory/roles", gotPath)
	}
}

func TestDirectoryHealthFailsWhenTheADSReturnsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := adsclient.New(server.URL)
	if err := client.DirectoryHealth(t.Context(), "admin-token"); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}
