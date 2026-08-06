package capabilitycatalog_test

import (
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"github.com/tishan-harischandra/cerbos-poc/libs/cataloggen"
)

func fixtureManifest(t *testing.T) *cataloggen.Manifest {
	t.Helper()
	raw := []byte(`
catalogRevision: 1
actions:
  - key: create
    displayName: Create
    context: COLLECTION
  - key: list
    displayName: List
    context: COLLECTION
  - key: read
    displayName: Read
    context: INSTANCE
  - key: update
    displayName: Update
    context: INSTANCE
  - key: delete
    displayName: Delete
    context: INSTANCE
  - key: assign
    displayName: Assign
    context: INSTANCE
resources:
  - fhirType: Observation
    domain: diagnostics
  - fhirType: Condition
    domain: clinical
  - fhirType: MedicationRequest
    domain: medications
`)
	m, err := cataloggen.ParseManifest(raw)
	if err != nil {
		t.Fatalf("parsing fixture manifest: %v", err)
	}
	return m
}

func TestSelectArchetypeResourcesIsSortedAndBounded(t *testing.T) {
	m := fixtureManifest(t)
	resources := capabilitycatalog.SelectArchetypeResources(m, 2)
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}
	if resources[0].ResourceKey >= resources[1].ResourceKey {
		t.Errorf("resources are not sorted: %q before %q", resources[0].ResourceKey, resources[1].ResourceKey)
	}
}

func TestGenerateArchetypeCapabilitiesProducesFiveKeysPerResource(t *testing.T) {
	m := fixtureManifest(t)
	resources := capabilitycatalog.SelectArchetypeResources(m, 3)
	defs := capabilitycatalog.GenerateArchetypeCapabilities(resources, 1)

	if len(defs) != 3*capabilitycatalog.ArchetypeCount {
		t.Fatalf("expected %d capabilities, got %d", 3*capabilitycatalog.ArchetypeCount, len(defs))
	}

	wantSuffixes := []string{".route.list", ".route.details", ".route.edit", ".button.assign", ".action.delete"}
	byKey := map[string]capabilitycatalog.UiCapabilityDefinition{}
	for _, d := range defs {
		byKey[d.Key] = d
	}
	for _, r := range resources {
		for _, suffix := range wantSuffixes {
			if _, ok := byKey[r.ResourceKey+suffix]; !ok {
				t.Errorf("missing generated capability %s%s", r.ResourceKey, suffix)
			}
		}
	}
}

func TestGenerateArchetypeCapabilitiesIsDeterministic(t *testing.T) {
	m := fixtureManifest(t)
	resources := capabilitycatalog.SelectArchetypeResources(m, 3)

	first := capabilitycatalog.GenerateArchetypeCapabilities(resources, 1)
	second := capabilitycatalog.GenerateArchetypeCapabilities(resources, 1)

	if len(first) != len(second) {
		t.Fatalf("length differs across runs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Key != second[i].Key {
			t.Fatalf("key order differs at %d: %q vs %q", i, first[i].Key, second[i].Key)
		}
	}
}

func TestGenerateArchetypeCapabilitiesShareLeavesAcrossResources(t *testing.T) {
	m := fixtureManifest(t)
	resources := capabilitycatalog.SelectArchetypeResources(m, 3)
	defs := capabilitycatalog.GenerateArchetypeCapabilities(resources, 1)

	counts := capabilitycatalog.CountLeafOccurrences(defs)

	// hospital_context:read is required by every X.route.list, so with 3
	// resources it must appear 3 times: real, measurable sharing.
	if counts["hospital_context:read"] != len(resources) {
		t.Errorf("expected hospital_context:read to be used %d times, got %d",
			len(resources), counts["hospital_context:read"])
	}

	// Every resource's own X:read leaf is reused as the "related" leaf of
	// its predecessor's route.details capability, so with 3 resources every
	// X:read leaf should appear at least twice.
	for _, r := range resources {
		key := r.ResourceKey + ":read"
		if counts[key] < 2 {
			t.Errorf("expected %s to be shared at least twice, got %d", key, counts[key])
		}
	}
}

func TestGenerateArchetypeCapabilitiesValidateAgainstTheirOwnLeaves(t *testing.T) {
	m := fixtureManifest(t)
	resources := capabilitycatalog.SelectArchetypeResources(m, 3)
	defs := capabilitycatalog.GenerateArchetypeCapabilities(resources, 1)

	catalog := capabilitycatalog.NewActiveCatalog()
	catalog.Add(capabilitycatalog.HospitalContextResource, "read")
	for _, r := range resources {
		for _, action := range []string{"list", "read", "update", "delete", "assign"} {
			catalog.Add(r.ResourceKey, action)
		}
	}

	if errs := capabilitycatalog.Validate(defs, catalog); len(errs) != 0 {
		t.Fatalf("expected the generated capabilities to validate cleanly, got %v", errs)
	}
}
