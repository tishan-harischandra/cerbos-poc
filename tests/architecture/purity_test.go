package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// The constraint: capabilityeval is a pure function of the data handed to it.
// It folds leaf outcomes into a composite verdict and nothing else. The moment
// it can read a file, open a socket or query a database it stops being
// exhaustively testable in memory, and the §12.2 composite evaluation acquires
// a second source of truth that no property test can reach.
func TestCapabilityEvalHasNoIODependency(t *testing.T) {
	root := repoRoot(t)
	packageDir := filepath.Join(root, architecture.PureEvaluationPackage)

	entries, err := os.ReadDir(packageDir)
	if err != nil {
		t.Fatalf("reading %s: %v", packageDir, err)
	}

	// Test files are excluded: the package's own property test drives it
	// with a random source, which is exactly the kind of ambient input the
	// production code may not have. Forbidding it in the test as well would
	// forbid testing the property.
	var scanned int
	var findings []architecture.Finding
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		relative := filepath.Join(architecture.PureEvaluationPackage, name)
		source, err := os.ReadFile(filepath.Join(packageDir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", relative, err)
		}

		fileFindings, err := architecture.ScanForIODependency(relative, string(source))
		if err != nil {
			t.Fatalf("scanning %s: %v", relative, err)
		}
		findings = append(findings, fileFindings...)
		scanned++
	}

	if scanned == 0 {
		t.Fatalf("no non-test Go files under %s; the scan would pass vacuously",
			architecture.PureEvaluationPackage)
	}

	if len(findings) > 0 {
		var report strings.Builder
		report.WriteString("the composite capability evaluator acquired an I/O dependency:\n")
		for _, finding := range findings {
			report.WriteString("  " + finding.String() + "\n")
		}
		t.Fatal(report.String())
	}
}

// An architecture test that cannot fail is decoration.
func TestTheCheckerCatchesAnIODependency(t *testing.T) {
	const violation = `package capabilityeval

import (
	"fmt"
	"os"
)

// The defect: the evaluator reaching past its arguments for the catalog.
func loadDefinition(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return string(raw), nil
}
`

	findings, err := architecture.ScanForIODependency("libs/capabilityeval/load.go", violation)
	if err != nil {
		t.Fatalf("ScanForIODependency: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1; got %v", len(findings), findings)
	}
	if findings[0].Symbol != "os" {
		t.Errorf("finding names %q, want the os import", findings[0].Symbol)
	}
}

// The evaluator does import the catalog package, which can itself read files.
// What it may not do is call that half: taking the vocabulary is fine, reaching
// for the loader is the dependency this rule forbids.
func TestTheCheckerCatchesACatalogLoaderCall(t *testing.T) {
	const violation = `package capabilityeval

import "github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"

func definitions(dir string) ([]capabilitycatalog.Definition, error) {
	return capabilitycatalog.LoadDefinitionsDir(dir)
}
`

	findings, err := architecture.ScanForIODependency("libs/capabilityeval/definitions.go", violation)
	if err != nil {
		t.Fatalf("ScanForIODependency: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1; got %v", len(findings), findings)
	}
	if findings[0].Symbol != "capabilitycatalog.LoadDefinitionsDir" {
		t.Errorf("finding names %q, want the catalog loader call", findings[0].Symbol)
	}
}

// Pure dependencies stay allowed, or the rule would just forbid writing code.
func TestTheCheckerAllowsPureDependencies(t *testing.T) {
	const pure = `package capabilityeval

import (
	"fmt"
	"sort"

	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
)

func describe(expression capabilitycatalog.Expression, ids []string) string {
	sort.Strings(ids)
	return fmt.Sprintf("%v %v", expression.Kind, ids)
}
`

	findings, err := architecture.ScanForIODependency("libs/capabilityeval/describe.go", pure)
	if err != nil {
		t.Fatalf("ScanForIODependency: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("a pure file was flagged: %v", findings)
	}
}
