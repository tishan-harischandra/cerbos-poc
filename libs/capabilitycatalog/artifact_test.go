package capabilitycatalog_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

const artifactModuleYAML = `
catalogRevision: 4
capabilities:
  - key: clinical.chart.view
    module: clinical
    context: INSTANCE
    expression:
      permission:
        resource: patient_record
        action: read
        targetRef: patient
`

func writeModule(t *testing.T, dir, module, body string) string {
	t.Helper()
	moduleDir := filepath.Join(dir, module)
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", moduleDir, err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "generated.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", moduleDir, err)
	}
	return moduleDir
}

// ADR-013 measured 100,000 capabilities at 4.0 s and 838 MiB from YAML
// against 203 ms and 90 MiB from a pre-decoded artifact. The artifact is
// only worth having if the loader actually prefers it, so prove the
// preference by making the artifact hold something the YAML does not.
func TestLoadDefinitionsForModulePrefersThePreDecodedArtifact(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "clinical", artifactModuleYAML)

	fromArtifact := []capabilitycatalog.UiCapabilityDefinition{
		{Key: "clinical.served.from.artifact", Module: "clinical", Context: "INSTANCE", CatalogRevision: 4},
	}
	if err := capabilitycatalog.WriteModuleArtifact(dir, "clinical", fromArtifact); err != nil {
		t.Fatalf("WriteModuleArtifact: %v", err)
	}

	defs, err := capabilitycatalog.LoadDefinitionsForModule(dir, "clinical")
	if err != nil {
		t.Fatalf("LoadDefinitionsForModule: %v", err)
	}
	if len(defs) != 1 || defs[0].Key != "clinical.served.from.artifact" {
		t.Fatalf("defs = %+v, want the artifact's content", defs)
	}
}

// A stale artifact is the one way this optimisation could serve content the
// adopter never authored. It must be ignored, not trusted.
func TestAStaleArtifactIsIgnoredAndTheYAMLServesInstead(t *testing.T) {
	dir := t.TempDir()
	moduleDir := writeModule(t, dir, "clinical", artifactModuleYAML)

	if err := capabilitycatalog.WriteModuleArtifact(dir, "clinical",
		[]capabilitycatalog.UiCapabilityDefinition{
			{Key: "clinical.stale", Module: "clinical", Context: "INSTANCE"},
		}); err != nil {
		t.Fatalf("WriteModuleArtifact: %v", err)
	}

	// The adopter edits the module after the artifact was emitted.
	time.Sleep(10 * time.Millisecond)
	edited := artifactModuleYAML + `  - key: clinical.chart.edit
    module: clinical
    context: INSTANCE
    expression:
      permission:
        resource: patient_record
        action: update
        targetRef: patient
`
	if err := os.WriteFile(filepath.Join(moduleDir, "generated.yaml"), []byte(edited), 0o644); err != nil {
		t.Fatalf("editing the module: %v", err)
	}

	defs, err := capabilitycatalog.LoadDefinitionsForModule(dir, "clinical")
	if err != nil {
		t.Fatalf("LoadDefinitionsForModule: %v", err)
	}
	for _, d := range defs {
		if d.Key == "clinical.stale" {
			t.Fatalf("a stale artifact was served: %+v", defs)
		}
	}
	if len(defs) != 2 {
		t.Fatalf("defs = %d, want the 2 capabilities the edited YAML declares: %+v", len(defs), defs)
	}
}

// A corrupt artifact must degrade to the YAML rather than failing the read:
// the artifact is an optimisation, never the system of record.
func TestACorruptArtifactFallsBackToTheYAML(t *testing.T) {
	dir := t.TempDir()
	moduleDir := writeModule(t, dir, "clinical", artifactModuleYAML)
	if err := os.WriteFile(filepath.Join(moduleDir, capabilitycatalog.ArtifactFileName),
		[]byte("this is not a gob stream"), 0o644); err != nil {
		t.Fatalf("writing the corrupt artifact: %v", err)
	}

	defs, err := capabilitycatalog.LoadDefinitionsForModule(dir, "clinical")
	if err != nil {
		t.Fatalf("LoadDefinitionsForModule: %v", err)
	}
	if len(defs) != 1 || defs[0].Key != "clinical.chart.view" {
		t.Fatalf("defs = %+v, want the YAML's content", defs)
	}
}

// The artifact must survive a round trip with its expression tree intact,
// or it would serve structurally different capabilities than the YAML.
func TestTheArtifactRoundTripsAnExpressionTree(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "clinical", artifactModuleYAML)

	want := []capabilitycatalog.UiCapabilityDefinition{{
		Key: "clinical.composite", Module: "clinical", Context: "INSTANCE", CatalogRevision: 4,
		Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
			{Permission: &capabilitycatalog.PermissionRequirement{
				Resource: "patient_record", Action: "read", TargetRef: "patient"}},
			{CapabilityRef: "clinical.chart.view"},
		}},
	}}
	if err := capabilitycatalog.WriteModuleArtifact(dir, "clinical", want); err != nil {
		t.Fatalf("WriteModuleArtifact: %v", err)
	}

	defs, err := capabilitycatalog.LoadDefinitionsForModule(dir, "clinical")
	if err != nil {
		t.Fatalf("LoadDefinitionsForModule: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("defs = %d, want 1", len(defs))
	}
	if len(defs[0].Expression.AllOf) != 2 {
		t.Fatalf("allOf = %+v, want 2 children", defs[0].Expression.AllOf)
	}
	if defs[0].Expression.AllOf[0].Permission == nil ||
		defs[0].Expression.AllOf[0].Permission.Resource != "patient_record" {
		t.Errorf("the permission leaf did not survive: %+v", defs[0].Expression.AllOf[0])
	}
	if defs[0].Expression.AllOf[1].CapabilityRef != "clinical.chart.view" {
		t.Errorf("the capabilityRef did not survive: %+v", defs[0].Expression.AllOf[1])
	}
}
