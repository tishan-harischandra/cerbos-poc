package capability_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/capability"
)

// fixtureModules is the catalog laid out the way the generator writes it:
// one directory per module, so a reader can take one without the rest
// (ADR-013).
var fixtureModules = map[string]string{
	"clinical": `
catalogRevision: 1
capabilities:
  - key: patient.route.details
    module: clinical
    context: INSTANCE
    expression:
      permission:
        resource: patient_record
        action: read
        targetRef: patient
`,
	"financial": `
catalogRevision: 1
capabilities:
  - key: account.route.list
    module: financial
    context: COLLECTION
    expression:
      permission:
        resource: account
        action: list
        targetRef: accountCollection
`,
}

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for module, body := range fixtureModules {
		moduleDir := filepath.Join(dir, module)
		if err := os.MkdirAll(moduleDir, 0o755); err != nil {
			t.Fatalf("creating %s: %v", moduleDir, err)
		}
		if err := os.WriteFile(filepath.Join(moduleDir, "generated.yaml"), []byte(body), 0o644); err != nil {
			t.Fatalf("writing fixture: %v", err)
		}
	}
	return dir
}

func TestFSCatalogReturnsOnlyTheRequestedModulesDefinitions(t *testing.T) {
	catalog := capability.NewFSCatalog(writeFixture(t), 12, nil)

	defs, revision, err := catalog.Definitions(context.Background(), "clinical")
	if err != nil {
		t.Fatalf("Definitions: %v", err)
	}
	if revision != "ui-capabilities-v12" {
		t.Errorf("revision = %q, want %q", revision, "ui-capabilities-v12")
	}
	if len(defs) != 1 || defs[0].Key != "patient.route.details" {
		t.Errorf("defs = %+v, want exactly patient.route.details", defs)
	}
}

func TestFSCatalogServesFromCacheOnASecondCall(t *testing.T) {
	dir := writeFixture(t)
	catalog := capability.NewFSCatalog(dir, 1, nil)

	if _, _, err := catalog.Definitions(context.Background(), "financial"); err != nil {
		t.Fatalf("Definitions: %v", err)
	}

	// Remove the source directory: a second call must still succeed,
	// proving it was served from the cache rather than reading disk again.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("removing fixture dir: %v", err)
	}

	defs, _, err := catalog.Definitions(context.Background(), "financial")
	if err != nil {
		t.Fatalf("Definitions after removing the source directory: %v", err)
	}
	if len(defs) != 1 || defs[0].Key != "account.route.list" {
		t.Errorf("defs = %+v, want exactly account.route.list", defs)
	}
}

// The OOMKill ADR-013 measured is a whole-catalog parse on the first request
// after a pod start. Serving one module must cost one module, proven by
// making every other module unparseable: a catalog that still reads them
// fails here instead of failing in production at 60,000 capabilities.
func TestFSCatalogDoesNotParseOtherModulesToServeOne(t *testing.T) {
	dir := writeFixture(t)
	poisoned := filepath.Join(dir, "financial", "generated.yaml")
	if err := os.WriteFile(poisoned, []byte("capabilities: [not: valid yaml\n"), 0o644); err != nil {
		t.Fatalf("poisoning the sibling module: %v", err)
	}

	catalog := capability.NewFSCatalog(dir, 12, nil)

	defs, _, err := catalog.Definitions(context.Background(), "clinical")
	if err != nil {
		t.Fatalf("serving clinical parsed the financial module: %v", err)
	}
	if len(defs) != 1 || defs[0].Key != "patient.route.details" {
		t.Errorf("defs = %+v, want exactly patient.route.details", defs)
	}
}
