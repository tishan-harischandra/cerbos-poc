package capabilityeval_test

import (
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilityeval"
)

func permission(resource, action, targetRef string) capabilitycatalog.Expression {
	return capabilitycatalog.Expression{Permission: &capabilitycatalog.PermissionRequirement{
		Resource: resource, Action: action, TargetRef: targetRef,
	}}
}

func TestFlattenReturnsOneLeafPerPermission(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		{Key: "patient.route.details", Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
			permission("patient_record", "read", "patient"),
		}}},
	}
	targets := map[string]capabilityeval.ResourceRef{
		"patient": {Kind: "patient_record", ID: "patient-456"},
	}

	leaves, err := capabilityeval.Flatten(defs, targets)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if len(leaves) != 1 {
		t.Fatalf("expected 1 leaf, got %d", len(leaves))
	}
	want := capabilityeval.Leaf{Resource: capabilityeval.ResourceRef{Kind: "patient_record", ID: "patient-456"}, Action: "read"}
	if leaves[0] != want {
		t.Errorf("got %+v, want %+v", leaves[0], want)
	}
}

func TestFlattenDeduplicatesALeafSharedAcrossCapabilities(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		{Key: "patient.route.details", Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
			permission("patient_record", "read", "patient"),
		}}},
		{Key: "patient.route.edit", Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
			permission("patient_record", "read", "patient"),
			permission("patient_record", "update", "patient"),
		}}},
	}
	targets := map[string]capabilityeval.ResourceRef{
		"patient": {Kind: "patient_record", ID: "patient-456"},
	}

	leaves, err := capabilityeval.Flatten(defs, targets)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if len(leaves) != 2 {
		t.Fatalf("expected the shared read leaf to be checked exactly once (2 unique leaves total), got %d: %+v", len(leaves), leaves)
	}
}

func TestFlattenWalksNestedAnyOfInsideAllOf(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		{Key: "patient.button.create-order", Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
			permission("patient_record", "read", "patient"),
			{AnyOf: []capabilitycatalog.Expression{
				permission("medication_request", "create", "medicationOrders"),
				permission("service_request", "create", "laboratoryOrders"),
			}},
		}}},
	}
	targets := map[string]capabilityeval.ResourceRef{
		"patient":          {Kind: "patient_record", ID: "patient-456"},
		"medicationOrders": {Kind: "medication_request", ID: "hospital-1"},
		"laboratoryOrders": {Kind: "service_request", ID: "hospital-1"},
	}

	leaves, err := capabilityeval.Flatten(defs, targets)
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if len(leaves) != 3 {
		t.Fatalf("expected 3 unique leaves, got %d: %+v", len(leaves), leaves)
	}
}

func TestFlattenFailsOnAnUnresolvedTargetRef(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		{Key: "patient.route.details", Expression: permission("patient_record", "read", "patient")},
	}

	_, err := capabilityeval.Flatten(defs, map[string]capabilityeval.ResourceRef{})
	if err == nil {
		t.Fatal("expected an error for an unresolved targetRef")
	}
}
