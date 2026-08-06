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
