package cataloggen_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/cataloggen"
)

func TestPascalToSnake(t *testing.T) {
	cases := map[string]string{
		"Patient":                 "patient",
		"AllergyIntolerance":      "allergy_intolerance",
		"ImagingStudy":            "imaging_study",
		"MedicationRequest":       "medication_request",
		"DeviceUsage":             "device_usage",
		"OrganizationAffiliation": "organization_affiliation",
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

func TestLoadManifestFileOnTheCommittedManifestIsValid(t *testing.T) {
	m, err := cataloggen.LoadManifestFile("manifest.yaml")
	if err != nil {
		t.Fatalf("LoadManifestFile: %v", err)
	}
	if len(m.IncludedResources()) == 0 {
		t.Fatalf("expected the real manifest to include at least one resource")
	}
	if len(m.Actions) != 6 {
		t.Fatalf("expected 6 actions in the real manifest, got %d", len(m.Actions))
	}
}

// Reading from a path must add no transformation of its own: the manifest a
// caller gets from a file is the manifest ParseManifest derives from the very
// same bytes, so moving the generators off the embedded copy cannot change
// what they generate.
func TestLoadManifestFileMatchesParseManifestOnTheSameBytes(t *testing.T) {
	raw, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatalf("reading the committed manifest: %v", err)
	}
	want, err := cataloggen.ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}

	got, err := cataloggen.LoadManifestFile("manifest.yaml")
	if err != nil {
		t.Fatalf("LoadManifestFile: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadManifestFile applied a transformation ParseManifest does not")
	}
}

// A manifest that is not where the caller said it is must fail naming that
// path, and must not fall back to any other manifest: once the domain model is
// adopter data (ADR-013) a silent fallback would generate the wrong catalog
// rather than refusing to generate one.
func TestLoadManifestFileOnAMissingPathNamesThePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")

	_, err := cataloggen.LoadManifestFile(path)
	if err == nil {
		t.Fatalf("expected an error for a missing manifest path")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the path %q", err, path)
	}
}

func TestLoadManifestFileOnAMalformedManifestNamesThePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed.yaml")
	if err := os.WriteFile(path, []byte("actions: [unterminated\n"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	_, err := cataloggen.LoadManifestFile(path)
	if err == nil {
		t.Fatalf("expected an error for a malformed manifest")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the path %q", err, path)
	}
}
