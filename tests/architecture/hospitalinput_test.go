package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// No code reads a hospital from a request header or query parameter (issue
// #78): the hospital comes only from the verified token's organization
// claim.
func TestNoConsumerReadsAHospitalFromRequestInput(t *testing.T) {
	root := repoRoot(t)

	var findings []architecture.Finding
	for _, path := range goFiles(t, root) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relativising %s: %v", path, err)
		}
		if strings.HasPrefix(filepath.ToSlash(relative), "tests/architecture/") {
			continue
		}

		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		found, err := architecture.ScanForRequestDerivedHospital(relative, string(source))
		if err != nil {
			t.Fatalf("scanning %s: %v", relative, err)
		}
		findings = append(findings, found...)
	}

	if len(findings) > 0 {
		var report strings.Builder
		report.WriteString("a hospital was read from request input:\n")
		for _, finding := range findings {
			report.WriteString("  " + finding.String() + "\n")
		}
		t.Fatal(report.String())
	}
}

// An architecture test that cannot fail is decoration.
func TestTheCheckerCatchesAHospitalReadFromAHeader(t *testing.T) {
	const violation = `package pep

func handle(r *http.Request) {
	hospitalID := r.Header.Get("X-Hospital-Id")
	_ = hospitalID
}
`

	findings, err := architecture.ScanForRequestDerivedHospital("apps/resource-service/internal/pep/pep.go", violation)
	if err != nil {
		t.Fatalf("ScanForRequestDerivedHospital: %v", err)
	}
	if len(findings) != 1 || findings[0].Symbol != "Get" {
		t.Errorf("findings = %v, want one for the header read", findings)
	}
}

// An architecture test that cannot fail is decoration.
func TestTheCheckerCatchesAHospitalReadFromAQueryParameter(t *testing.T) {
	const violation = `package pep

func handle(r *http.Request) {
	hospitalID := r.URL.Query().Get("hospitalId")
	_ = hospitalID
}
`

	findings, err := architecture.ScanForRequestDerivedHospital("apps/resource-service/internal/pep/pep.go", violation)
	if err != nil {
		t.Fatalf("ScanForRequestDerivedHospital: %v", err)
	}
	if len(findings) != 1 || findings[0].Symbol != "Get" {
		t.Errorf("findings = %v, want one for the query read", findings)
	}
}

// The audit search endpoint's hospital is a search filter, authority-checked
// against the caller's own scope, not an identity-derivation shortcut - it
// is a named exception, not a gap the checker missed.
func TestTheCheckerExemptsTheAuditSearchFilter(t *testing.T) {
	const source = `package auditsearch

func handle(r *http.Request) {
	hospital := r.URL.Query().Get("hospital")
	_ = hospital
}
`

	findings, err := architecture.ScanForRequestDerivedHospital(
		"apps/admin-service/internal/auditsearch/handler.go", source)
	if err != nil {
		t.Fatalf("ScanForRequestDerivedHospital: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("the audit search filter's exemption did not apply: %v", findings)
	}
}

// An ordinary header or query read that has nothing to do with a hospital
// must not be flagged.
func TestTheCheckerAllowsOrdinaryHeaderAndQueryReads(t *testing.T) {
	const source = `package pep

func handle(r *http.Request) {
	correlationID := r.Header.Get("X-Correlation-Id")
	query := r.URL.Query().Get("query")
	_, _ = correlationID, query
}
`

	findings, err := architecture.ScanForRequestDerivedHospital("apps/resource-service/internal/pep/pep.go", source)
	if err != nil {
		t.Fatalf("ScanForRequestDerivedHospital: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("ordinary header/query reads were flagged: %v", findings)
	}
}
