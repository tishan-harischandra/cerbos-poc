package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// No dialect-specific SQL outside the driver adapters. The port exists so the
// second engine is a configuration change rather than a rewrite, and that only
// holds while service logic stays ignorant of which engine it is talking to.
func TestNoDialectSpecificSQLOutsideTheAdapters(t *testing.T) {
	root := repoRoot(t)

	var findings []architecture.Finding
	for _, path := range goFiles(t, root) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relativising %s: %v", path, err)
		}
		// The checker itself names every marker it hunts for, as do its tests.
		if strings.HasPrefix(filepath.ToSlash(relative), "tests/architecture/") {
			continue
		}

		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		findings = append(findings, architecture.ScanForDialectLeak(
			relative, string(source), architecture.IsDialectAdapter(relative))...)
	}

	if len(findings) > 0 {
		var report strings.Builder
		report.WriteString("dialect-specific SQL found outside the driver adapters:\n")
		for _, finding := range findings {
			report.WriteString("  " + finding.String() + "\n")
		}
		t.Fatal(report.String())
	}
}

// An architecture test that cannot fail is decoration.
func TestTheCheckerCatchesADialectLeak(t *testing.T) {
	const leak = `package matrix

func save(db *sql.DB) error {
	_, err := db.Exec("INSERT INTO role_permission (tenant_id) VALUES ($1) ON CONFLICT (tenant_id) DO NOTHING")
	return err
}
`

	findings := architecture.ScanForDialectLeak("apps/ads/internal/matrix/save.go", leak, false)
	if len(findings) == 0 {
		t.Fatal("the checker did not catch an upsert clause outside the adapters")
	}
	if findings[0].Symbol != "ON CONFLICT" {
		t.Errorf("finding named %q, want the ON CONFLICT marker", findings[0].Symbol)
	}
}

func TestTheCheckerAllowsDialectSQLInsideAnAdapter(t *testing.T) {
	const adapterSource = `package oraclestore

const statement = "MERGE INTO role_permission t USING (SELECT :1 FROM dual) s ON (t.tenant_id = s.tenant_id)"
`

	relative := "libs/assignmentstore/oraclestore/oraclestore.go"
	if !architecture.IsDialectAdapter(relative) {
		t.Fatalf("%s should be recognised as a driver adapter", relative)
	}

	findings := architecture.ScanForDialectLeak(relative, adapterSource,
		architecture.IsDialectAdapter(relative))
	if len(findings) != 0 {
		t.Errorf("the adapter itself was flagged: %v", findings)
	}
}

// The port must stay clean too: it is the file every caller reads to learn the
// vocabulary, so a dialect name there would teach the wrong thing.
func TestThePortNamesNoDialect(t *testing.T) {
	root := repoRoot(t)
	relative := filepath.Join("libs", "assignmentstore", "store.go")

	source, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		t.Fatalf("reading the port: %v", err)
	}

	findings := architecture.ScanForDialectLeak(relative, string(source), false)
	if len(findings) > 0 {
		t.Errorf("the store port names a dialect: %v", findings)
	}
}
