package simulate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/adsclient"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/simulate"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
)

func adminOf(tenant, hospital string) tokenauth.Identity {
	return tokenauth.Identity{
		PrincipalID: "admin-1", TenantID: tenant, HospitalID: hospital,
		Roles: []string{"kc:tenant-a:realm:administrator"}, RawToken: "admin-token",
	}
}

func post(target, body string, identity tokenauth.Identity) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(tokenauth.WithIdentity(req.Context(), identity))
}

type fakeADS struct {
	accessReq  adsclient.SimulateAccessRequest
	accessResp adsclient.SimulateAccessResponse
	accessErr  error

	capsReq  adsclient.SimulateCapabilitiesRequest
	capsResp adsclient.SimulateCapabilitiesResponse
	capsErr  error

	lastToken string
}

func (f *fakeADS) SimulateAccess(_ context.Context, token string, req adsclient.SimulateAccessRequest) (adsclient.SimulateAccessResponse, error) {
	f.lastToken = token
	f.accessReq = req
	return f.accessResp, f.accessErr
}

func (f *fakeADS) SimulateCapabilities(_ context.Context, token string, req adsclient.SimulateCapabilitiesRequest) (adsclient.SimulateCapabilitiesResponse, error) {
	f.lastToken = token
	f.capsReq = req
	return f.capsResp, f.capsErr
}

const accessBody = `{
  "tenantId": "tenant-a",
  "hospitalId": "hospital-1",
  "principalId": "user-doctor",
  "idpRoles": ["kc:tenant-a:realm:doctor"],
  "resource": {"kind": "patient_record", "id": "patient-456", "attributes": {"status": "ACTIVE"}},
  "action": "read"
}`

// The simulator forwards the caller's own bearer token to the ADS, never
// minting a credential of its own.
func TestSimulateAccessForwardsTheAdminsOwnToken(t *testing.T) {
	ads := &fakeADS{accessResp: adsclient.SimulateAccessResponse{Allowed: true, Source: "ROLE"}}
	handler := simulate.Handler{ADS: ads}

	rec := httptest.NewRecorder()
	handler.SimulateAccess(rec, post("/admin/authz/simulate", accessBody, adminOf("tenant-a", "hospital-1")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if ads.lastToken != "admin-token" {
		t.Errorf("token forwarded to the ADS = %q, want admin-token", ads.lastToken)
	}
	if ads.accessReq.PrincipalID != "user-doctor" {
		t.Errorf("principalId forwarded = %q, want user-doctor", ads.accessReq.PrincipalID)
	}

	var body adsclient.SimulateAccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if !body.Allowed {
		t.Error("allowed = false, want true")
	}
}

// §9.4's authority check: an administrator may simulate only within the
// tenant and hospital their own token scopes them to.
func TestSimulateAccessRejectsACrossHospitalRequest(t *testing.T) {
	ads := &fakeADS{}
	handler := simulate.Handler{ADS: ads}

	rec := httptest.NewRecorder()
	handler.SimulateAccess(rec, post("/admin/authz/simulate", accessBody, adminOf("tenant-a", "hospital-2")))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
	if ads.accessReq.PrincipalID != "" {
		t.Error("the ADS was called for an unauthorized cross-hospital simulation")
	}
}

func TestSimulateAccessRequiresAVerifiedIdentity(t *testing.T) {
	ads := &fakeADS{}
	handler := simulate.Handler{ADS: ads}

	req := httptest.NewRequest(http.MethodPost, "/admin/authz/simulate", strings.NewReader(accessBody))
	rec := httptest.NewRecorder()
	handler.SimulateAccess(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestSimulateAccessSurfacesAnADSFailureWithoutLeakingTheCredential(t *testing.T) {
	adminSecret := "a-secret-nobody-should-see"
	ads := &fakeADS{accessErr: errFromADS(adminSecret)}
	handler := simulate.Handler{ADS: ads}

	rec := httptest.NewRecorder()
	handler.SimulateAccess(rec, post("/admin/authz/simulate", accessBody, adminOf("tenant-a", "hospital-1")))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), adminSecret) {
		t.Errorf("the response echoed the credential: %s", rec.Body)
	}
}

const capabilitiesBody = `{
  "module": "clinical",
  "capabilityKeys": ["patient.route.edit"],
  "tenantId": "tenant-a",
  "hospitalId": "hospital-1",
  "principalId": "user-doctor",
  "idpRoles": ["kc:tenant-a:realm:doctor"],
  "sampleAttributes": {"patient": {"status": "ACTIVE"}}
}`

// Capability simulation returns the full requirement tree, exactly what
// the ADS reported - the admin-service adds no evaluation of its own.
func TestSimulateCapabilitiesReturnsTheFullRequirementTree(t *testing.T) {
	ads := &fakeADS{capsResp: adsclient.SimulateCapabilitiesResponse{
		Capabilities: map[string]adsclient.CapabilityResult{"patient.route.edit": {Allowed: true}},
		RequirementTree: []adsclient.LeafDecision{
			{Resource: "patient_record", Action: "read", Target: "sample:patient", Allowed: true, Reason: "ROLE"},
			{Resource: "patient_record", Action: "update", Target: "sample:patient", Allowed: true, Reason: "ROLE"},
		},
	}}
	handler := simulate.Handler{ADS: ads}

	rec := httptest.NewRecorder()
	handler.SimulateCapabilities(rec, post("/admin/authz/simulate-capabilities", capabilitiesBody, adminOf("tenant-a", "hospital-1")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body adsclient.SimulateCapabilitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(body.RequirementTree) != 2 {
		t.Fatalf("requirementTree = %d leaves, want 2", len(body.RequirementTree))
	}
}

func TestSimulateCapabilitiesRejectsACrossTenantRequest(t *testing.T) {
	ads := &fakeADS{}
	handler := simulate.Handler{ADS: ads}

	rec := httptest.NewRecorder()
	handler.SimulateCapabilities(rec, post("/admin/authz/simulate-capabilities", capabilitiesBody, adminOf("tenant-b", "hospital-1")))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
}

type simpleError string

func (e simpleError) Error() string { return string(e) }

func errFromADS(secret string) error {
	return simpleError("adsclient: the ADS returned 503: client_secret=" + secret)
}
