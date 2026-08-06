package capabilitycatalog_test

import (
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"gopkg.in/yaml.v3"
)

func decodeDefinition(t *testing.T, raw string) (capabilitycatalog.UiCapabilityDefinition, error) {
	t.Helper()
	var d capabilitycatalog.UiCapabilityDefinition
	err := yaml.Unmarshal([]byte(raw), &d)
	return d, err
}

func TestUnmarshalPermissionLeaf(t *testing.T) {
	d, err := decodeDefinition(t, `
key: patient.route.details
module: clinical
context: INSTANCE
expression:
  permission:
    resource: patient_record
    action: read
    targetRef: patient
`)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if d.Expression.Permission == nil {
		t.Fatalf("expected a permission leaf, got %+v", d.Expression)
	}
	if d.Expression.Permission.Resource != "patient_record" || d.Expression.Permission.Action != "read" {
		t.Errorf("unexpected permission leaf: %+v", d.Expression.Permission)
	}
}

func TestUnmarshalNestedAnyOfInsideAllOf(t *testing.T) {
	d, err := decodeDefinition(t, `
key: patient.button.create-order
module: clinical
context: INSTANCE
expression:
  allOf:
    - permission:
        resource: patient_record
        action: read
        targetRef: patient
    - anyOf:
        - permission:
            resource: medication_request
            action: create
            targetRef: medicationRequestCollection
        - permission:
            resource: service_request
            action: create
            targetRef: serviceRequestCollection
`)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(d.Expression.AllOf) != 2 {
		t.Fatalf("expected 2 allOf items, got %d", len(d.Expression.AllOf))
	}
	nested := d.Expression.AllOf[1]
	if len(nested.AnyOf) != 2 {
		t.Fatalf("expected a nested anyOf with 2 items, got %+v", nested)
	}
}

func TestUnmarshalRejectsEmptyAllOf(t *testing.T) {
	_, err := decodeDefinition(t, `
key: broken.capability
module: clinical
context: INSTANCE
expression:
  allOf: []
`)
	if err == nil {
		t.Fatal("expected an error for an empty allOf array")
	}
	if !strings.Contains(err.Error(), "broken.capability") {
		t.Errorf("error does not name the capability: %v", err)
	}
	if !strings.Contains(err.Error(), "allOf") {
		t.Errorf("error does not mention allOf: %v", err)
	}
}

func TestUnmarshalRejectsEmptyAnyOf(t *testing.T) {
	_, err := decodeDefinition(t, `
key: broken.capability
module: clinical
context: INSTANCE
expression:
  anyOf: []
`)
	if err == nil {
		t.Fatal("expected an error for an empty anyOf array")
	}
	if !strings.Contains(err.Error(), "broken.capability") {
		t.Errorf("error does not name the capability: %v", err)
	}
}

func TestUnmarshalRejectsNegation(t *testing.T) {
	_, err := decodeDefinition(t, `
key: broken.capability
module: clinical
context: INSTANCE
expression:
  not:
    permission:
      resource: patient_record
      action: read
      targetRef: patient
`)
	if err == nil {
		t.Fatal("expected an error for a negation construct")
	}
	if !strings.Contains(err.Error(), "broken.capability") {
		t.Errorf("error does not name the capability: %v", err)
	}
	if !strings.Contains(err.Error(), "negation") {
		t.Errorf("error does not call out negation: %v", err)
	}
}

func TestUnmarshalRejectsUnknownExpressionKey(t *testing.T) {
	_, err := decodeDefinition(t, `
key: broken.capability
module: clinical
context: INSTANCE
expression:
  someOtherThing:
    resource: patient_record
`)
	if err == nil {
		t.Fatal("expected an error for an unknown expression key")
	}
}

func TestUnmarshalRejectsAmbiguousExpression(t *testing.T) {
	_, err := decodeDefinition(t, `
key: broken.capability
module: clinical
context: INSTANCE
expression:
  permission:
    resource: patient_record
    action: read
    targetRef: patient
  allOf:
    - permission:
        resource: patient_record
        action: update
        targetRef: patient
`)
	if err == nil {
		t.Fatal("expected an error for an expression with more than one recognised key")
	}
}
