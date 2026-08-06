package capabilitycatalog_test

import (
	"path/filepath"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

func TestLoadDefinitionsDirMergesAndSortsAcrossFiles(t *testing.T) {
	defs, err := capabilitycatalog.LoadDefinitionsDir(filepath.Join("testdata", "definitions"))
	if err != nil {
		t.Fatalf("LoadDefinitionsDir: %v", err)
	}
	if len(defs) != 3 {
		t.Fatalf("expected 3 definitions across both fixture files, got %d", len(defs))
	}
	for i := 1; i < len(defs); i++ {
		if defs[i-1].Key >= defs[i].Key {
			t.Fatalf("definitions are not sorted by key: %q before %q", defs[i-1].Key, defs[i].Key)
		}
	}
}

func TestLoadDefinitionsDirStampsCatalogRevision(t *testing.T) {
	defs, err := capabilitycatalog.LoadDefinitionsDir(filepath.Join("testdata", "definitions"))
	if err != nil {
		t.Fatalf("LoadDefinitionsDir: %v", err)
	}
	for _, d := range defs {
		if d.CatalogRevision != 1 {
			t.Errorf("capability %q: expected catalog revision 1, got %d", d.Key, d.CatalogRevision)
		}
	}
}

func TestLoadActiveCatalogDirBuildsResourceActionPairs(t *testing.T) {
	catalog, err := capabilitycatalog.LoadActiveCatalogDir(filepath.Join("testdata", "catalog"))
	if err != nil {
		t.Fatalf("LoadActiveCatalogDir: %v", err)
	}
	if !catalog.Has("patient_record", "read") {
		t.Error("expected patient_record:read to be known")
	}
	if !catalog.Has("medication_request", "list") {
		t.Error("expected medication_request:list to be known")
	}
	if catalog.Has("patient_record", "delete") {
		t.Error("did not expect patient_record:delete to be known")
	}
	if catalog.Has("no_such_resource", "read") {
		t.Error("did not expect an unknown resource to be known")
	}
}

func TestLoadResourceCatalogDirReadsDisplayMetadata(t *testing.T) {
	entries, err := capabilitycatalog.LoadResourceCatalogDir(filepath.Join("testdata", "catalog"))
	if err != nil {
		t.Fatalf("LoadResourceCatalogDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	byKey := make(map[string]capabilitycatalog.ResourceEntry, len(entries))
	for _, entry := range entries {
		byKey[entry.ResourceKey] = entry
	}

	patient, ok := byKey["patient_record"]
	if !ok {
		t.Fatal("patient_record entry is missing")
	}
	if patient.DisplayName != "Patient record" || patient.Domain != "clinical" {
		t.Errorf("patient_record = %+v, want displayName=Patient record domain=clinical", patient)
	}
	if len(patient.Actions) != 2 {
		t.Fatalf("patient_record has %d actions, want 2", len(patient.Actions))
	}
	if patient.Actions[0].Key != "read" || patient.Actions[0].DisplayName != "View patient" || patient.Actions[0].Context != "INSTANCE" {
		t.Errorf("patient_record.Actions[0] = %+v, want key=read displayName='View patient' context=INSTANCE", patient.Actions[0])
	}
}

// The resource catalog module needs risk metadata to browse by (issue
// #18, §6.1), so the loader must carry it through rather than dropping
// it on the floor between the YAML file and ResourceEntry.
func TestLoadResourceCatalogDirReadsRiskMetadata(t *testing.T) {
	entries, err := capabilitycatalog.LoadResourceCatalogDir(filepath.Join("testdata", "catalog"))
	if err != nil {
		t.Fatalf("LoadResourceCatalogDir: %v", err)
	}

	byKey := make(map[string]capabilitycatalog.ResourceEntry, len(entries))
	for _, entry := range entries {
		byKey[entry.ResourceKey] = entry
	}
	patient, ok := byKey["patient_record"]
	if !ok {
		t.Fatal("patient_record entry is missing")
	}
	if patient.Actions[0].Risk != "STANDARD" {
		t.Errorf("patient_record read risk = %q, want STANDARD", patient.Actions[0].Risk)
	}
	if patient.Actions[1].Risk != "ELEVATED" {
		t.Errorf("patient_record update risk = %q, want ELEVATED", patient.Actions[1].Risk)
	}
}

func TestLoadResourceCatalogDirIsSortedByResourceKey(t *testing.T) {
	entries, err := capabilitycatalog.LoadResourceCatalogDir(filepath.Join("testdata", "catalog"))
	if err != nil {
		t.Fatalf("LoadResourceCatalogDir: %v", err)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].ResourceKey >= entries[i].ResourceKey {
			t.Fatalf("entries are not sorted: %q before %q", entries[i-1].ResourceKey, entries[i].ResourceKey)
		}
	}
}

func TestLoadedDefinitionsValidateAgainstLoadedCatalog(t *testing.T) {
	defs, err := capabilitycatalog.LoadDefinitionsDir(filepath.Join("testdata", "definitions"))
	if err != nil {
		t.Fatalf("LoadDefinitionsDir: %v", err)
	}
	catalog, err := capabilitycatalog.LoadActiveCatalogDir(filepath.Join("testdata", "catalog"))
	if err != nil {
		t.Fatalf("LoadActiveCatalogDir: %v", err)
	}
	if errs := capabilitycatalog.Validate(defs, catalog); len(errs) != 0 {
		t.Fatalf("expected the fixture definitions to validate cleanly, got %v", errs)
	}
}
