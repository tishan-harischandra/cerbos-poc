package cataloggen_test

import (
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/cataloggen"
)

func TestPascalToSnake(t *testing.T) {
	cases := map[string]string{
		"Patient":                  "patient",
		"AllergyIntolerance":       "allergy_intolerance",
		"ImagingStudy":             "imaging_study",
		"MedicationRequest":        "medication_request",
		"DeviceUsage":              "device_usage",
		"OrganizationAffiliation":  "organization_affiliation",
	}
	for input, want := range cases {
		if got := cataloggen.PascalToSnake(input); got != want {
			t.Errorf("PascalToSnake(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		"AllergyIntolerance": "Allergy intolerance",
		"Patient":            "Patient",
		"MedicationRequest":  "Medication request",
	}
	for input, want := range cases {
		if got := cataloggen.DisplayName(input); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseManifestDerivesResourceKeyAndDisplayName(t *testing.T) {
	m, err := cataloggen.ParseManifest([]byte(`
catalogRevision: 1
actions:
  - key: read
    displayName: Read
    context: INSTANCE
resources:
  - fhirType: AllergyIntolerance
    domain: clinical
`))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(m.Resources))
	}
	entry := m.Resources[0]
	if entry.ResourceKey != "allergy_intolerance" {
		t.Errorf("ResourceKey = %q, want allergy_intolerance", entry.ResourceKey)
	}
	if entry.Display != "Allergy intolerance" {
		t.Errorf("Display = %q, want %q", entry.Display, "Allergy intolerance")
	}
	if !entry.IsIncluded() {
		t.Errorf("expected entry to default to included")
	}
}

func TestParseManifestRejectsExclusionWithoutReason(t *testing.T) {
	_, err := cataloggen.ParseManifest([]byte(`
catalogRevision: 1
actions:
  - key: read
    displayName: Read
    context: INSTANCE
resources:
  - fhirType: Patient
    domain: administrative
    included: false
`))
	if err == nil {
		t.Fatalf("expected an error for an excluded resource with no reason")
	}
	if !strings.Contains(err.Error(), "records no reason") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseManifestRejectsDuplicateResourceKeys(t *testing.T) {
	_, err := cataloggen.ParseManifest([]byte(`
catalogRevision: 1
actions:
  - key: read
    displayName: Read
    context: INSTANCE
resources:
  - fhirType: Condition
    domain: clinical
  - fhirType: Condition
    domain: clinical
`))
	if err == nil {
		t.Fatalf("expected an error for a duplicate fhirType")
	}
}

func TestParseManifestRejectsUnknownLockableAction(t *testing.T) {
	_, err := cataloggen.ParseManifest([]byte(`
catalogRevision: 1
actions:
  - key: read
    displayName: Read
    context: INSTANCE
lockableActions: [update]
resources:
  - fhirType: Condition
    domain: clinical
`))
	if err == nil {
		t.Fatalf("expected an error for a lockable action that is not a declared action")
	}
}

func TestLoadEmbeddedManifestIsValid(t *testing.T) {
	m, err := cataloggen.LoadEmbeddedManifest()
	if err != nil {
		t.Fatalf("LoadEmbeddedManifest: %v", err)
	}
	if len(m.IncludedResources()) == 0 {
		t.Fatalf("expected the real manifest to include at least one resource")
	}
	if len(m.Actions) != 6 {
		t.Fatalf("expected 6 actions in the real manifest, got %d", len(m.Actions))
	}
}
