package capabilitycatalog_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

func TestRenderDefinitionsYAMLRoundTripsThroughTheLoader(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		{
			Key:     "patient_record.route.list",
			Module:  "clinical",
			Context: "COLLECTION",
			Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
				{Permission: &capabilitycatalog.PermissionRequirement{Resource: "patient_record", Action: "list", TargetRef: "patient_recordCollection"}},
				{Permission: &capabilitycatalog.PermissionRequirement{Resource: "hospital_context", Action: "read", TargetRef: "hospitalContext"}},
			}},
			CatalogRevision: 1,
		},
		{
			Key:     "patient.button.create-order",
			Module:  "clinical",
			Context: "INSTANCE",
			Expression: capabilitycatalog.Expression{AllOf: []capabilitycatalog.Expression{
				{Permission: &capabilitycatalog.PermissionRequirement{Resource: "patient_record", Action: "read", TargetRef: "patient"}},
				{AnyOf: []capabilitycatalog.Expression{
					{Permission: &capabilitycatalog.PermissionRequirement{Resource: "medication_request", Action: "create", TargetRef: "medicationOrderCollection"}},
					{Permission: &capabilitycatalog.PermissionRequirement{Resource: "service_request", Action: "create", TargetRef: "laboratoryOrderCollection"}},
				}},
			}},
			CatalogRevision: 1,
		},
	}

	rendered := capabilitycatalog.RenderDefinitionsYAML(1, defs)

	dir := t.TempDir()
	path := filepath.Join(dir, "generated.yaml")
	if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
		t.Fatalf("writing rendered file: %v", err)
	}

	loaded, err := capabilitycatalog.LoadDefinitionsDir(dir)
	if err != nil {
		t.Fatalf("LoadDefinitionsDir: %v", err)
	}
	if len(loaded) != len(defs) {
		t.Fatalf("expected %d definitions after round-trip, got %d", len(defs), len(loaded))
	}

	byKey := map[string]capabilitycatalog.UiCapabilityDefinition{}
	for _, d := range loaded {
		byKey[d.Key] = d
	}
	for _, want := range defs {
		got, ok := byKey[want.Key]
		if !ok {
			t.Fatalf("capability %q missing after round-trip", want.Key)
		}
		if !reflect.DeepEqual(got.Expression, want.Expression) {
			t.Errorf("capability %q expression changed across round-trip:\nwant %+v\ngot  %+v", want.Key, want.Expression, got.Expression)
		}
	}
}

func TestRenderDefinitionsYAMLIsDeterministic(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		{
			Key:        "a.route.list",
			Module:     "clinical",
			Context:    "COLLECTION",
			Expression: capabilitycatalog.Expression{Permission: &capabilitycatalog.PermissionRequirement{Resource: "a", Action: "list", TargetRef: "aCollection"}},
		},
	}
	first := capabilitycatalog.RenderDefinitionsYAML(1, defs)
	second := capabilitycatalog.RenderDefinitionsYAML(1, defs)
	if first != second {
		t.Fatal("RenderDefinitionsYAML produced different output for the same input")
	}
}

func TestRenderSeedCSVProducesOneRowPerCapabilityWithCatalogRevision(t *testing.T) {
	defs := []capabilitycatalog.UiCapabilityDefinition{
		{
			Key:             "patient_record.route.list",
			Module:          "clinical",
			Context:         "COLLECTION",
			CatalogRevision: 7,
			Expression:      capabilitycatalog.Expression{Permission: &capabilitycatalog.PermissionRequirement{Resource: "patient_record", Action: "list", TargetRef: "x"}},
		},
	}
	out, err := capabilitycatalog.RenderSeedCSV(defs)
	if err != nil {
		t.Fatalf("RenderSeedCSV: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected a header row and one data row, got %d lines", len(lines))
	}
	if !strings.Contains(lines[1], "patient_record.route.list") {
		t.Errorf("data row missing capability key: %q", lines[1])
	}
	if !strings.Contains(lines[1], ",7,") {
		t.Errorf("data row missing catalog revision 7: %q", lines[1])
	}
	if !strings.Contains(lines[1], `"permission"`) {
		t.Errorf("data row missing the expression JSON: %q", lines[1])
	}
}
