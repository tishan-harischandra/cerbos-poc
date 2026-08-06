package capabilitycatalog_test

import (
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

// repoCatalogResourcesDir and repoCapabilitiesDir are relative to this
// package's directory, which is where `go test` sets the working directory.
const (
	repoCatalogResourcesDir = "../../deploy/cerbos/catalog/resources"
	repoCapabilitiesDir     = "../../deploy/cerbos/catalog/ui-capabilities"
)

// loadFullCapabilitySet loads the full committed capability set: the
// generated archetype definitions and the five hand-authored §12.1 worked
// examples both live as files directly under
// deploy/cerbos/catalog/ui-capabilities, so LoadDefinitionsDir alone
// already returns the merged, 400-capability set.
func loadFullCapabilitySet(t *testing.T) []capabilitycatalog.UiCapabilityDefinition {
	t.Helper()

	defs, err := capabilitycatalog.LoadDefinitionsDir(repoCapabilitiesDir)
	if err != nil {
		t.Fatalf("loading committed capability definitions: %v", err)
	}
	return defs
}

// TestExactlyFourHundredCapabilities is the issue #10 acceptance criterion
// "Exactly 400 capabilities are generated and committed".
func TestExactlyFourHundredCapabilities(t *testing.T) {
	defs := loadFullCapabilitySet(t)
	if len(defs) != 400 {
		t.Fatalf("expected exactly 400 capabilities, got %d", len(defs))
	}
}

// TestTheFiveWorkedExamplesArePresent proves the §12.1 worked examples,
// including the nested anyOf inside patient.button.create-order, are part
// of the committed set.
func TestTheFiveWorkedExamplesArePresent(t *testing.T) {
	defs := loadFullCapabilitySet(t)
	byKey := map[string]capabilitycatalog.UiCapabilityDefinition{}
	for _, d := range defs {
		byKey[d.Key] = d
	}

	wantKeys := []string{
		"patients.route.list",
		"patient.route.details",
		"patient.route.edit",
		"patient.component.clinical-summary",
		"patient.button.create-order",
	}
	for _, key := range wantKeys {
		if _, ok := byKey[key]; !ok {
			t.Errorf("missing worked example capability %q", key)
		}
	}

	createOrder, ok := byKey["patient.button.create-order"]
	if !ok {
		t.Fatal("patient.button.create-order is missing")
	}
	foundNestedAnyOf := false
	for _, child := range createOrder.Expression.AllOf {
		if len(child.AnyOf) > 0 {
			foundNestedAnyOf = true
		}
	}
	if !foundNestedAnyOf {
		t.Error("expected patient.button.create-order to contain a nested anyOf inside its allOf")
	}
}

// TestLeafSharingAcrossTheCommittedCatalogIsNonTrivial is the issue #10
// acceptance criterion "Leaf sharing across capabilities is measurable and
// non-trivial".
func TestLeafSharingAcrossTheCommittedCatalogIsNonTrivial(t *testing.T) {
	defs := loadFullCapabilitySet(t)
	counts := capabilitycatalog.CountLeafOccurrences(defs)

	if got := counts["hospital_context:read"]; got < capabilitycatalog.ArchetypeResourceCount {
		t.Errorf("expected hospital_context:read to be shared at least %d times, got %d",
			capabilitycatalog.ArchetypeResourceCount, got)
	}

	sharedAtLeastTwice := 0
	for _, count := range counts {
		if count >= 2 {
			sharedAtLeastTwice++
		}
	}
	if sharedAtLeastTwice < 50 {
		t.Errorf("expected at least 50 distinct leaves reused across capabilities, got %d", sharedAtLeastTwice)
	}
}

// TestTheCommittedCatalogValidatesCleanly is the issue #10 CI validation
// gate itself: everything generated plus everything hand-authored must
// resolve against the real, committed resource catalog.
func TestTheCommittedCatalogValidatesCleanly(t *testing.T) {
	defs := loadFullCapabilitySet(t)

	catalog, err := capabilitycatalog.LoadActiveCatalogDir(repoCatalogResourcesDir)
	if err != nil {
		t.Fatalf("loading the committed resource catalog: %v", err)
	}

	if errs := capabilitycatalog.Validate(defs, catalog); len(errs) != 0 {
		t.Fatalf("the committed capability catalog does not validate: %v", errs)
	}
}
