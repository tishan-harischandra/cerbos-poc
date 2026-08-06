// The UI capability catalog drift and validation gate (issue #10, §12.1,
// §12.2). Mirrors catalogdrift_test.go: the generated archetype capability
// definitions are regenerated in memory from the committed manifest and
// diffed against what is on disk, and the full set (generated plus
// hand-authored §12.1 worked examples) is validated against the committed
// resource catalog, so a manifest change, a hand-edit of the generated
// file, or an invalid hand-authored definition fails CI instead of silently
// drifting or shipping a broken capability.
package architecture_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"github.com/tishan-harischandra/cerbos-poc/libs/cataloggen"
)

const (
	capabilitiesDir      = "deploy/cerbos/catalog/ui-capabilities"
	capabilityCatalogDir = "deploy/cerbos/catalog/resources"
)

// TestGeneratedCapabilityCatalogMatchesTheManifest is the drift half of the
// gate: run `make capability-gen` at the repo root if this fails.
func TestGeneratedCapabilityCatalogMatchesTheManifest(t *testing.T) {
	manifest, err := cataloggen.LoadEmbeddedManifest()
	if err != nil {
		t.Fatalf("loading the committed manifest: %v", err)
	}

	root := repoRoot(t)

	resources := capabilitycatalog.SelectArchetypeResources(manifest, capabilitycatalog.ArchetypeResourceCount)
	generated := capabilitycatalog.GenerateArchetypeCapabilities(resources, manifest.CatalogRevision)
	want := capabilitycatalog.RenderDefinitionsYAML(manifest.CatalogRevision, generated)

	generatedPath := filepath.Join(root, capabilitiesDir, "generated.yaml")
	got, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("reading %s: %v", generatedPath, err)
	}

	if string(got) != want {
		t.Fatalf("%s does not match libs/capabilitycatalog's archetype generator; "+
			"run `make capability-gen` at the repo root", generatedPath)
	}
}

// TestTheFullCapabilityCatalogIsExactlyFourHundredAndValidates is the
// validation half of the gate and the issue #10 acceptance criteria
// "Exactly 400 capabilities are generated and committed" and "CI fails the
// build on any validation violation".
func TestTheFullCapabilityCatalogIsExactlyFourHundredAndValidates(t *testing.T) {
	root := repoRoot(t)

	defs, err := capabilitycatalog.LoadDefinitionsDir(filepath.Join(root, capabilitiesDir))
	if err != nil {
		t.Fatalf("loading committed capability definitions: %v", err)
	}
	if len(defs) != 400 {
		t.Fatalf("expected exactly 400 capabilities, got %d", len(defs))
	}

	catalog, err := capabilitycatalog.LoadActiveCatalogDir(filepath.Join(root, capabilityCatalogDir))
	if err != nil {
		t.Fatalf("loading the committed resource catalog: %v", err)
	}
	if errs := capabilitycatalog.Validate(defs, catalog); len(errs) > 0 {
		t.Fatalf("the committed UI capability catalog does not validate: %v", errs)
	}
}
