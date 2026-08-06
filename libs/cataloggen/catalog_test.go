package cataloggen_test

import (
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/cataloggen"
)

// The catalog is the administration-facing metadata source the resource
// catalog module renders (issue #18, §6.1's "labels, descriptions,
// grouping, context type and risk metadata"). A lockable action carries
// more consequence than a non-lockable one, so the catalog must say so
// with the same classification the DB seed's risk_level column already
// uses, rather than leaving risk out of the file the browser reads.
func TestRenderCatalogEntryMarksLockableActionsAsElevatedRisk(t *testing.T) {
	m, err := cataloggen.ParseManifest([]byte(`
catalogRevision: 1
actions:
  - key: read
    displayName: Read
    context: INSTANCE
  - key: update
    displayName: Update
    context: INSTANCE
lockableActions: [update]
resources:
  - fhirType: Condition
    domain: clinical
`))
	if err != nil {
		t.Fatalf("parsing manifest: %v", err)
	}

	entry := m.IncludedResources()[0]
	rendered := cataloggen.RenderCatalogEntry(m, entry)

	if !strings.Contains(rendered, "key: read\n    displayName: Read condition\n    context: INSTANCE\n    risk: STANDARD\n") {
		t.Errorf("read action was not rendered as STANDARD risk:\n%s", rendered)
	}
	if !strings.Contains(rendered, "key: update\n    displayName: Update condition\n    context: INSTANCE\n    risk: ELEVATED\n") {
		t.Errorf("update action was not rendered as ELEVATED risk (it is lockable):\n%s", rendered)
	}
}
