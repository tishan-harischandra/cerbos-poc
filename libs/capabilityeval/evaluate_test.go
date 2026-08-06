package capabilityeval_test

import (
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilityeval"
)

func leaf(kind, id, action string) capabilityeval.Leaf {
	return capabilityeval.Leaf{Resource: capabilityeval.ResourceRef{Kind: kind, ID: id}, Action: action}
}

func TestEvaluateAllOfIsAllowedOnlyWhenEveryLeafIsAllowed(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		{Key: "patient.route.edit", Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
			permission("patient_record", "read", "patient"),
			permission("patient_record", "update", "patient"),
		}}},
	}
	targets := map[string]capabilityeval.ResourceRef{"patient": {Kind: "patient_record", ID: "p-1"}}

	allAllowed := map[capabilityeval.Leaf]capabilityeval.LeafOutcome{
		leaf("patient_record", "p-1", "read"):   {Allowed: true},
		leaf("patient_record", "p-1", "update"): {Allowed: true},
	}
	outcomes, err := capabilityeval.Evaluate(defs, targets, allAllowed)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !outcomes["patient.route.edit"].Allowed {
		t.Error("expected the capability to be allowed when every leaf is allowed")
	}

	oneRevoked := map[capabilityeval.Leaf]capabilityeval.LeafOutcome{
		leaf("patient_record", "p-1", "read"):   {Allowed: true},
		leaf("patient_record", "p-1", "update"): {Allowed: false, Reason: "USER_REVOKE"},
	}
	outcomes, err = capabilityeval.Evaluate(defs, targets, oneRevoked)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcomes["patient.route.edit"].Allowed {
		t.Error("expected the capability to be denied when one required leaf is denied")
	}
}

func TestEvaluateAnyOfIsAllowedWhenAtLeastOneLeafIsAllowed(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		{Key: "patient.button.create-order", Expression: capabilitycatalog.Expression{AnyOf: []capabilitycatalog.Expression{
			permission("medication_request", "create", "meds"),
			permission("service_request", "create", "labs"),
		}}},
	}
	targets := map[string]capabilityeval.ResourceRef{
		"meds": {Kind: "medication_request", ID: "h-1"},
		"labs": {Kind: "service_request", ID: "h-1"},
	}

	decisions := map[capabilityeval.Leaf]capabilityeval.LeafOutcome{
		leaf("medication_request", "h-1", "create"): {Allowed: false, Reason: "USER_REVOKE"},
		leaf("service_request", "h-1", "create"):    {Allowed: true},
	}
	outcomes, err := capabilityeval.Evaluate(defs, targets, decisions)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !outcomes["patient.button.create-order"].Allowed {
		t.Error("expected anyOf to be allowed when at least one leaf is allowed")
	}

	noneAllowed := map[capabilityeval.Leaf]capabilityeval.LeafOutcome{
		leaf("medication_request", "h-1", "create"): {Allowed: false, Reason: "USER_REVOKE"},
		leaf("service_request", "h-1", "create"):    {Allowed: false, Reason: "MANDATORY_RULE"},
	}
	outcomes, err = capabilityeval.Evaluate(defs, targets, noneAllowed)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcomes["patient.button.create-order"].Allowed {
		t.Error("expected anyOf to be denied when every leaf is denied")
	}
}

func TestEvaluateNestedAnyOfInsideAllOf(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		{Key: "patient.button.create-order", Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
			permission("patient_record", "read", "patient"),
			{AnyOf: []capabilitycatalog.Expression{
				permission("medication_request", "create", "meds"),
				permission("service_request", "create", "labs"),
			}},
		}}},
	}
	targets := map[string]capabilityeval.ResourceRef{
		"patient": {Kind: "patient_record", ID: "p-1"},
		"meds":    {Kind: "medication_request", ID: "h-1"},
		"labs":    {Kind: "service_request", ID: "h-1"},
	}

	// Read is allowed, and one of the two anyOf branches is allowed: the
	// whole allOf must be allowed.
	decisions := map[capabilityeval.Leaf]capabilityeval.LeafOutcome{
		leaf("patient_record", "p-1", "read"):       {Allowed: true},
		leaf("medication_request", "h-1", "create"): {Allowed: false, Reason: "USER_REVOKE"},
		leaf("service_request", "h-1", "create"):    {Allowed: true},
	}
	outcomes, err := capabilityeval.Evaluate(defs, targets, decisions)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !outcomes["patient.button.create-order"].Allowed {
		t.Error("expected the allOf to be allowed: read passed and the nested anyOf had one allowed branch")
	}

	// Read is allowed but both anyOf branches are denied: the allOf must
	// fail, and only the anyOf's leaves belong in the failure evidence
	// since the read leaf did not cause the failure.
	bothOrdersDenied := map[capabilityeval.Leaf]capabilityeval.LeafOutcome{
		leaf("patient_record", "p-1", "read"):       {Allowed: true},
		leaf("medication_request", "h-1", "create"): {Allowed: false, Reason: "USER_REVOKE"},
		leaf("service_request", "h-1", "create"):    {Allowed: false, Reason: "MANDATORY_RULE"},
	}
	outcomes, err = capabilityeval.Evaluate(defs, targets, bothOrdersDenied)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	outcome := outcomes["patient.button.create-order"]
	if outcome.Allowed {
		t.Fatal("expected the capability to be denied")
	}
	if len(outcome.FailedRequirements) != 2 {
		t.Fatalf("expected exactly the 2 denied anyOf leaves as evidence, got %d: %+v",
			len(outcome.FailedRequirements), outcome.FailedRequirements)
	}
	for _, req := range outcome.FailedRequirements {
		if req.Resource == "patient_record" {
			t.Errorf("the passing read leaf must not appear in failure evidence: %+v", req)
		}
	}
}

func TestEvaluateFailsOnAnUncheckedLeaf(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		{Key: "patient.route.details", Expression: permission("patient_record", "read", "patient")},
	}
	targets := map[string]capabilityeval.ResourceRef{"patient": {Kind: "patient_record", ID: "p-1"}}

	// No decisions supplied at all: a leaf the caller never checked must
	// never silently evaluate to allowed.
	outcomes, err := capabilityeval.Evaluate(defs, targets, map[capabilityeval.Leaf]capabilityeval.LeafOutcome{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcomes["patient.route.details"].Allowed {
		t.Error("a leaf with no decision must never be treated as allowed")
	}
}
