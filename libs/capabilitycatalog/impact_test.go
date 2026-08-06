package capabilitycatalog_test

import (
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

func def(key, module string, expr capabilitycatalog.Expression) capabilitycatalog.UiCapabilityDefinition {
	return capabilitycatalog.UiCapabilityDefinition{Key: key, Module: module, Context: "INSTANCE", Expression: expr}
}

func permissionExpr(resource, action string) capabilitycatalog.Expression {
	return capabilitycatalog.Expression{Permission: &capabilitycatalog.PermissionRequirement{
		Resource: resource, Action: action, TargetRef: resource,
	}}
}

// The capability impact module (issue #18, §9.1) needs the reverse index
// from a resource-action leaf to every capability whose expression
// references it, at any depth of allOf/anyOf nesting - not just the top
// level.
func TestCapabilitiesReferencingFindsALeafNestedInsideAllOf(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		def("patient.route.edit", "clinical", capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
			permissionExpr("patient_record", "read"),
			permissionExpr("patient_record", "update"),
		}}),
		def("patient.route.view", "clinical", permissionExpr("patient_record", "read")),
		def("prescription.route.view", "pharmacy", permissionExpr("prescription", "read")),
	}

	got := capabilitycatalog.CapabilitiesReferencing(defs, "patient_record", "update")

	if len(got) != 1 || got[0].Key != "patient.route.edit" {
		t.Fatalf("CapabilitiesReferencing(patient_record, update) = %+v, want exactly [patient.route.edit]", got)
	}
}

// A leaf shared by multiple capabilities must return every one of them,
// sorted, so the impact list is deterministic for the same reason every
// other loader/generator output in this package is.
func TestCapabilitiesReferencingReturnsEveryCapabilitySharingALeafSorted(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		def("patient.route.view", "clinical", permissionExpr("patient_record", "read")),
		def("patient.component.summary", "clinical", capabilitycatalog.Expression{AnyOf: []capabilitycatalog.Expression{
			permissionExpr("patient_record", "read"),
			permissionExpr("patient_record", "list"),
		}}),
	}

	got := capabilitycatalog.CapabilitiesReferencing(defs, "patient_record", "read")

	if len(got) != 2 {
		t.Fatalf("expected 2 capabilities referencing patient_record:read, got %d: %+v", len(got), got)
	}
	if got[0].Key != "patient.component.summary" || got[1].Key != "patient.route.view" {
		t.Errorf("expected capabilities sorted by key, got %q then %q", got[0].Key, got[1].Key)
	}
}

// A resource-action no capability references must come back as an empty
// slice, never an error - issue #18's "A resource-action used by no
// capability is clearly shown as such rather than as an empty error".
func TestCapabilitiesReferencingReturnsEmptyNotErrorWhenNothingMatches(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		def("patient.route.view", "clinical", permissionExpr("patient_record", "read")),
	}

	got := capabilitycatalog.CapabilitiesReferencing(defs, "patient_record", "delete")

	if got == nil {
		t.Fatal("expected a non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected no capabilities, got %+v", got)
	}
}
