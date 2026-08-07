package capability_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/capability"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
)

// firingPDP answers every leaf as allowed unless named in denied, and
// reports the given fired rule names for every resource - the simulator
// tests need decision-source labelling, which recordingPDP does not
// exercise.
type firingPDP struct {
	denied     map[cerbosclient.Leaf]bool
	firedRules []string
	lastReq    cerbosclient.Request
}

func (p *firingPDP) Check(_ context.Context, req cerbosclient.Request) (cerbosclient.Result, error) {
	p.lastReq = req
	decisions := make(map[cerbosclient.Leaf]cerbosclient.Decision)
	firedRules := make(map[cerbosclient.ResourceRef][]string)
	for _, resource := range req.Resources {
		firedRules[resource.Resource] = p.firedRules
		for _, action := range resource.Actions {
			l := cerbosclient.Leaf{Resource: resource.Resource, Action: action}
			decisions[l] = cerbosclient.Decision{Allowed: !p.denied[l]}
		}
	}
	return cerbosclient.Result{CallID: "sim-call-1", Decisions: decisions, FiredRules: firedRules}, nil
}

func simulateCapabilitiesPost(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/capabilities/simulate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(tokenauth.WithIdentity(req.Context(), identity()))
}

const simulateCapabilitiesRequest = `{
  "module": "clinical",
  "capabilityKeys": ["patient.route.edit"],
  "tenantId": "tenant-a",
  "hospitalId": "hospital-1",
  "principalId": "user-doctor",
  "idpRoles": ["kc:cerbos-poc:patient-app:doctor"],
  "sampleAttributes": {
    "patient": {"status": "ACTIVE"}
  }
}`

// The simulator reuses evaluate() itself, so this asserts it produces the
// same composed answer that machinery already produces for the real
// endpoint (issue #19's "not a separate evaluation implementation").
func TestSimulateCapabilitiesComposesTheSameAsTheRuntime(t *testing.T) {
	catalog := fakeCatalog{
		revision: "1",
		defs: []capabilitycatalog.UiCapabilityDefinition{
			{
				Key: "patient.route.edit", Module: "clinical", Context: "INSTANCE",
				Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
					permission("patient_record", "read", "patient"),
					permission("patient_record", "update", "patient"),
				}},
			},
		},
	}
	pdp := &firingPDP{}
	handler := capability.NewSimulateHandler(capability.Config{
		PDP: pdp, CapabilityCatalog: catalog, Assignments: emptyAssignments{}, RootPolicyRevision: "root-v1.4.0",
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, simulateCapabilitiesPost(t, simulateCapabilitiesRequest))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body capability.SimulateSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if !body.Capabilities["patient.route.edit"].Allowed {
		t.Errorf("patient.route.edit = denied, want allowed: %+v", body.Capabilities)
	}
}

// The full requirement tree names every leaf's decision, allowed and
// denied alike - not only the ones that explain a denial.
func TestSimulateCapabilitiesReturnsTheFullRequirementTree(t *testing.T) {
	catalog := fakeCatalog{
		revision: "1",
		defs: []capabilitycatalog.UiCapabilityDefinition{
			{
				Key: "patient.route.edit", Module: "clinical", Context: "INSTANCE",
				Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
					permission("patient_record", "read", "patient"),
					permission("patient_record", "update", "patient"),
				}},
			},
		},
	}
	pdp := &firingPDP{denied: map[cerbosclient.Leaf]bool{
		{Resource: cerbosclient.ResourceRef{Kind: "patient_record", ID: "sample:patient"}, Action: "update"}: true,
	}}
	handler := capability.NewSimulateHandler(capability.Config{
		PDP: pdp, CapabilityCatalog: catalog, Assignments: emptyAssignments{}, RootPolicyRevision: "root-v1.4.0",
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, simulateCapabilitiesPost(t, simulateCapabilitiesRequest))

	var body capability.SimulateSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(body.RequirementTree) != 2 {
		t.Fatalf("requirement tree = %d leaves, want 2 (read and update): %+v", len(body.RequirementTree), body.RequirementTree)
	}
	byAction := make(map[string]capability.LeafDecision, 2)
	for _, leaf := range body.RequirementTree {
		byAction[leaf.Action] = leaf
	}
	if !byAction["read"].Allowed {
		t.Errorf("read leaf = denied, want allowed: %+v", byAction["read"])
	}
	if byAction["update"].Allowed {
		t.Errorf("update leaf = allowed, want denied: %+v", byAction["update"])
	}
	if body.Capabilities["patient.route.edit"].Allowed {
		t.Error("patient.route.edit = allowed, want denied (update failed)")
	}
}

// Sample attributes are trusted context supplied directly by the
// administrator, not resolved from a real resource - they must still
// reach the PDP exactly like a resolved target's attributes would.
func TestSimulateCapabilitiesForwardsSampleAttributesToThePDP(t *testing.T) {
	catalog := fakeCatalog{
		revision: "1",
		defs: []capabilitycatalog.UiCapabilityDefinition{
			{Key: "patient.route.view", Module: "clinical", Context: "INSTANCE",
				Expression: permission("patient_record", "read", "patient")},
		},
	}
	pdp := &firingPDP{}
	handler := capability.NewSimulateHandler(capability.Config{
		PDP: pdp, CapabilityCatalog: catalog, Assignments: emptyAssignments{}, RootPolicyRevision: "root-v1.4.0",
	})

	req := strings.Replace(simulateCapabilitiesRequest, `["patient.route.edit"]`, `["patient.route.view"]`, 1)
	handler.ServeHTTP(httptest.NewRecorder(), simulateCapabilitiesPost(t, req))

	if pdp.lastReq.Resources[0].Attr["status"] != "ACTIVE" {
		t.Errorf("sample attribute was not forwarded to the PDP: %#v", pdp.lastReq.Resources[0].Attr)
	}
}

func TestSimulateCapabilitiesRequiresAVerifiedIdentity(t *testing.T) {
	handler := capability.NewSimulateHandler(capability.Config{
		PDP: &firingPDP{}, CapabilityCatalog: fakeCatalog{}, Assignments: emptyAssignments{},
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/capabilities/simulate", strings.NewReader(simulateCapabilitiesRequest))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestSimulateCapabilitiesRejectsASimulatedReservedRole(t *testing.T) {
	handler := capability.NewSimulateHandler(capability.Config{
		PDP: &firingPDP{}, CapabilityCatalog: fakeCatalog{}, Assignments: emptyAssignments{},
	})

	req := strings.Replace(simulateCapabilitiesRequest,
		`["kc:cerbos-poc:patient-app:doctor"]`, `["sys:permission-evaluator"]`, 1)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, simulateCapabilitiesPost(t, req))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
