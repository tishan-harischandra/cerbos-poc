package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// The constraint issue #84 asks for explicitly: a membership list that can
// widen a decision is the same bug as a hospital claim that can be forged,
// so no decision path may read VerifiedToken.OtherHospitals.
func TestNoDecisionPathReadsOtherHospitals(t *testing.T) {
	root := repoRoot(t)

	var findings []architecture.Finding
	for _, path := range goFiles(t, root) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relativising %s: %v", path, err)
		}

		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		fileFindings, err := architecture.ScanForOtherHospitalsRead(
			relative, string(source), architecture.IsOtherHospitalsOwner(relative))
		if err != nil {
			t.Fatalf("scanning %s: %v", relative, err)
		}
		findings = append(findings, fileFindings...)
	}

	if len(findings) > 0 {
		var report strings.Builder
		report.WriteString("OtherHospitals was read outside its owning package:\n")
		for _, finding := range findings {
			report.WriteString("  " + finding.String() + "\n")
		}
		t.Fatal(report.String())
	}
}

// An architecture test that cannot fail is decoration.
func TestTheCheckerCatchesAnOtherHospitalsRead(t *testing.T) {
	const violation = `package authz

func widened(token tokenverifier.VerifiedToken) []string {
	return token.OtherHospitals
}
`

	findings, err := architecture.ScanForOtherHospitalsRead("apps/ads/internal/authz/authz.go", violation, false)
	if err != nil {
		t.Fatalf("ScanForOtherHospitalsRead: %v", err)
	}
	if len(findings) != 1 || findings[0].Symbol != "OtherHospitals" {
		t.Errorf("findings = %v, want one for the OtherHospitals read", findings)
	}
}

func TestThePackageDefiningOtherHospitalsMayReadIt(t *testing.T) {
	const owner = `package tokenverifier

func (v VerifiedToken) hasOtherHospitals() bool {
	return len(v.OtherHospitals) > 0
}
`

	findings, err := architecture.ScanForOtherHospitalsRead("libs/tokenverifier/tokenverifier.go", owner, true)
	if err != nil {
		t.Fatalf("ScanForOtherHospitalsRead: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("the owning package was flagged: %v", findings)
	}
}

func TestOtherHospitalsOwnershipRules(t *testing.T) {
	cases := map[string]bool{
		"libs/tokenverifier/tokenverifier.go":       true,
		"libs/tokenverifier/tokenverifier_test.go":  true,
		"apps/ads/internal/authz/authz.go":          false,
		"apps/resource-service/internal/pep/pep.go": false,
	}

	for path, want := range cases {
		if got := architecture.IsOtherHospitalsOwner(path); got != want {
			t.Errorf("IsOtherHospitalsOwner(%q) = %t, want %t", path, got, want)
		}
	}
}
