package capabilitycatalog

import (
	"os"
	"path/filepath"
	"testing"
)

// The artifact replaces the whole module directory for any reader that
// trusts it, so it has to carry everything that directory holds. Deriving
// it from the generator's output alone would silently drop the
// hand-authored §12.1 worked examples, which share the clinical module with
// generated archetypes - and nothing reading the artifact would ever see
// them missing.
func TestBuildModuleArtifactsCapturesHandAuthoredDefinitionsToo(t *testing.T) {
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "clinical")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("creating the module directory: %v", err)
	}

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(moduleDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write("generated.yaml", "catalogRevision: 4\ncapabilities:\n"+
		"  - key: clinical.generated\n    module: clinical\n    context: INSTANCE\n")
	write("worked-examples.yaml", "catalogRevision: 4\ncapabilities:\n"+
		"  - key: clinical.hand.authored\n    module: clinical\n    context: INSTANCE\n")

	if err := BuildModuleArtifacts(dir); err != nil {
		t.Fatalf("BuildModuleArtifacts: %v", err)
	}

	defs, ok := loadModuleArtifact(moduleDir)
	if !ok {
		t.Fatal("the artifact just written was not accepted by the loader")
	}

	got := map[string]bool{}
	for _, d := range defs {
		got[d.Key] = true
	}
	for _, want := range []string{"clinical.generated", "clinical.hand.authored"} {
		if !got[want] {
			t.Errorf("the artifact is missing %q; it holds %v", want, got)
		}
	}
}
