package authz_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

// The simulator names a target principal explicitly - the whole point is
// evaluating as someone other than the caller - so the request body
// carries identity fields the real decision endpoint refuses (issue #19).
const simulateRequest = `{
  "tenantId": "tenant-a",
  "hospitalId": "hospital-1",
  "principalId": "user-doctor",
  "idpRoles": ["kc:tenant-a:realm:doctor"],
  "resource": {
    "kind": "patient_record",
    "id": "patient-456",
    "attributes": {"status": "ACTIVE"}
  },
  "action": "read"
}`

func simulatePost(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/internal/authz/simulate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(tokenauth.WithIdentity(req.Context(), identityWithRoles("kc:tenant-a:realm:administrator")))
}

// The simulator reuses the exact same Assignments+PDP+DecisionSource path
// the runtime decision endpoint uses (issue #19's "not a separate
// evaluation implementation"), asserted the same way
// TestTheDecisionSourceComesFromThePDPsFiredRules asserts it for the real
// endpoint: the reported source comes from the PDP's own fired rules.
func TestSimulateReportsTheDecisionSourceFromThePDPsFiredRules(t *testing.T) {
	pdp := &recordingPDP{
		result: cerbosclient.Result{
			CallID: "call-sim-1",
			Decisions: map[cerbosclient.Leaf]cerbosclient.Decision{
				leaf("patient-456", "read"): {Allowed: true},
			},
			FiredRules: map[cerbosclient.ResourceRef][]string{
				{Kind: "patient_record", ID: "patient-456"}: {"grant_read_to_user"},
			},
		},
	}
	handler := authz.NewSimulateHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, simulatePost(simulateRequest))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body authz.SimulateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if !body.Allowed {
		t.Error("allowed = false, want true")
	}
	if body.Source != authz.SourceUserGrant {
		t.Errorf("source = %q, want %q", body.Source, authz.SourceUserGrant)
	}
	if body.CerbosCallID != "call-sim-1" {
		t.Errorf("cerbosCallId = %q, want call-sim-1", body.CerbosCallID)
	}
}

// A LOCKED sample attribute must be forwarded to the real Cerbos policy
// exactly like the runtime path forwards it, so the mandatory constraint
// fires for real rather than being simulated separately (issue #19's
// acceptance criterion naming this exact scenario).
func TestSimulateWithALockedAttributeReportsMandatoryRule(t *testing.T) {
	pdp := &recordingPDP{
		result: cerbosclient.Result{
			CallID: "call-sim-2",
			Decisions: map[cerbosclient.Leaf]cerbosclient.Decision{
				leaf("patient-456", "update"): {Allowed: false},
			},
			FiredRules: map[cerbosclient.ResourceRef][]string{
				{Kind: "patient_record", ID: "patient-456"}: {"locked_record_restriction"},
			},
		},
	}
	handler := authz.NewSimulateHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

	locked := strings.Replace(simulateRequest, `"status": "ACTIVE"`, `"status": "LOCKED"`, 1)
	locked = strings.Replace(locked, `"action": "read"`, `"action": "update"`, 1)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, simulatePost(locked))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if pdp.request.Resources[0].Attr["status"] != "LOCKED" {
		t.Fatalf("the sample attribute was not forwarded to the PDP: %#v", pdp.request.Resources[0].Attr)
	}
	var body authz.SimulateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.Allowed {
		t.Error("allowed = true, want false for a locked record")
	}
	if body.Source != authz.SourceMandatory {
		t.Errorf("source = %q, want %q", body.Source, authz.SourceMandatory)
	}
}

// The simulator writes nothing and advances no revision - it only reports
// the PermissionRevision the assignments were resolved at.
func TestSimulateReportsThePermissionRevisionWithoutWriting(t *testing.T) {
	pdp := &recordingPDP{result: cerbosclient.Result{CallID: "call-sim-3"}}
	handler := authz.NewSimulateHandler(authz.Config{
		PDP:         pdp,
		Assignments: fixedAssignments{input: permissioncontext.Input{Revision: 184}},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, simulatePost(simulateRequest))

	var body authz.SimulateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.PermissionRevision != 184 {
		t.Errorf("permissionRevision = %d, want 184", body.PermissionRevision)
	}
}

func TestSimulateRequiresAVerifiedIdentity(t *testing.T) {
	pdp := &recordingPDP{}
	handler := authz.NewSimulateHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

	req := httptest.NewRequest(http.MethodPost, "/internal/authz/simulate", strings.NewReader(simulateRequest))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if pdp.calls != 0 {
		t.Error("the PDP was called for an unauthenticated request")
	}
}

func TestSimulateRejectsASimulatedReservedRole(t *testing.T) {
	pdp := &recordingPDP{}
	handler := authz.NewSimulateHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

	withReservedRole := strings.Replace(simulateRequest,
		`"idpRoles": ["kc:tenant-a:realm:doctor"]`,
		`"idpRoles": ["sys:permission-evaluator"]`, 1)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, simulatePost(withReservedRole))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if pdp.calls != 0 {
		t.Error("the PDP was called for a simulated reserved role")
	}
}

func TestSimulateRejectsMalformedRequests(t *testing.T) {
	cases := map[string]string{
		"no tenantId":    strings.Replace(simulateRequest, `"tenantId": "tenant-a",`, "", 1),
		"no principalId": strings.Replace(simulateRequest, `"principalId": "user-doctor",`, "", 1),
		"no action":      strings.Replace(simulateRequest, `"action": "read"`, `"action": ""`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			pdp := &recordingPDP{}
			handler := authz.NewSimulateHandler(authz.Config{PDP: pdp, Assignments: emptyAssignments{}})

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, simulatePost(body))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}
