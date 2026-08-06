package capabilitycatalog_test

import (
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

func mustDecode(t *testing.T, raw string) capabilitycatalog.UiCapabilityDefinition {
	t.Helper()
	d, err := decodeDefinition(t, raw)
	if err != nil {
		t.Fatalf("decoding fixture: %v", err)
	}
	return d
}

func TestValidateAcceptsAKnownResourceAndAction(t *testing.T) {
	catalog := capabilitycatalog.NewActiveCatalog()
	catalog.Add("patient_record", "read")

	d := mustDecode(t, `
key: patient.route.details
module: clinical
context: INSTANCE
expression:
  permission:
    resource: patient_record
    action: read
    targetRef: patient
`)

	if errs := capabilitycatalog.Validate([]capabilitycatalog.UiCapabilityDefinition{d}, catalog); len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidateRejectsUnknownResource(t *testing.T) {
	catalog := capabilitycatalog.NewActiveCatalog()
	catalog.Add("patient_record", "read")

	d := mustDecode(t, `
key: patient.route.details
module: clinical
context: INSTANCE
expression:
  permission:
    resource: not_a_real_resource
    action: read
    targetRef: patient
`)

	errs := capabilitycatalog.Validate([]capabilitycatalog.UiCapabilityDefinition{d}, catalog)
	if len(errs) != 1 {
		t.Fatalf("expected exactly one error, got %v", errs)
	}
	if !strings.Contains(errs[0].Error(), "patient.route.details") || !strings.Contains(errs[0].Error(), "not_a_real_resource") {
		t.Errorf("error does not name the capability and the offending resource: %v", errs[0])
	}
}

func TestValidateRejectsUnknownAction(t *testing.T) {
	catalog := capabilitycatalog.NewActiveCatalog()
	catalog.Add("patient_record", "read")

	d := mustDecode(t, `
key: patient.route.details
module: clinical
context: INSTANCE
expression:
  permission:
    resource: patient_record
    action: not_a_real_action
    targetRef: patient
`)

	errs := capabilitycatalog.Validate([]capabilitycatalog.UiCapabilityDefinition{d}, catalog)
	if len(errs) != 1 {
		t.Fatalf("expected exactly one error, got %v", errs)
	}
	if !strings.Contains(errs[0].Error(), "patient.route.details") || !strings.Contains(errs[0].Error(), "not_a_real_action") {
		t.Errorf("error does not name the capability and the offending action: %v", errs[0])
	}
}

func TestValidateRejectsACircularCapabilityReference(t *testing.T) {
	catalog := capabilitycatalog.NewActiveCatalog()

	a := mustDecode(t, `
key: a
module: clinical
context: INSTANCE
expression:
  capabilityRef: b
`)
	b := mustDecode(t, `
key: b
module: clinical
context: INSTANCE
expression:
  capabilityRef: a
`)

	errs := capabilitycatalog.Validate([]capabilitycatalog.UiCapabilityDefinition{a, b}, catalog)
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "circular") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a circular reference error, got %v", errs)
	}
}

func TestValidateRejectsAReferenceToAnUnknownCapability(t *testing.T) {
	catalog := capabilitycatalog.NewActiveCatalog()

	a := mustDecode(t, `
key: a
module: clinical
context: INSTANCE
expression:
  capabilityRef: does-not-exist
`)

	errs := capabilitycatalog.Validate([]capabilitycatalog.UiCapabilityDefinition{a}, catalog)
	if len(errs) != 1 {
		t.Fatalf("expected exactly one error, got %v", errs)
	}
	if !strings.Contains(errs[0].Error(), "does-not-exist") {
		t.Errorf("error does not name the unknown reference: %v", errs[0])
	}
}

func TestValidateRejectsADuplicateCapabilityKey(t *testing.T) {
	catalog := capabilitycatalog.NewActiveCatalog()
	catalog.Add("patient_record", "read")

	d1 := mustDecode(t, `
key: dup
module: clinical
context: INSTANCE
expression:
  permission:
    resource: patient_record
    action: read
    targetRef: patient
`)
	d2 := mustDecode(t, `
key: dup
module: clinical
context: INSTANCE
expression:
  permission:
    resource: patient_record
    action: read
    targetRef: patient
`)

	errs := capabilitycatalog.Validate([]capabilitycatalog.UiCapabilityDefinition{d1, d2}, catalog)
	if len(errs) != 1 {
		t.Fatalf("expected exactly one error, got %v", errs)
	}
	if !strings.Contains(errs[0].Error(), "dup") {
		t.Errorf("error does not name the duplicated key: %v", errs[0])
	}
}
