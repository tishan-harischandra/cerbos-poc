package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// No consumer anywhere in the module derives a tenant from request input
// (issue #77): the tenant is always the realm that signed the verified
// token, never a claim, a mode or a per-request field.
func TestNoConsumerDerivesATenantFromAClaim(t *testing.T) {
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

		found, err := architecture.ScanForClaimDerivedTenant(relative, string(source))
		if err != nil {
			t.Fatalf("scanning %s: %v", relative, err)
		}
		findings = append(findings, found...)
	}

	if len(findings) > 0 {
		var report strings.Builder
		report.WriteString("a claim- or mode-selected tenant mapping reappeared:\n")
		for _, finding := range findings {
			report.WriteString("  " + finding.String() + "\n")
		}
		t.Fatal(report.String())
	}
}

// An architecture test that cannot fail is decoration.
func TestTheCheckerCatchesAReintroducedTenantMappingMode(t *testing.T) {
	const violation = `package tokenverifier

type TenantMappingMode string

const TenantMappingClaim TenantMappingMode = "CLAIM"
`

	findings, err := architecture.ScanForClaimDerivedTenant("libs/tokenverifier/tokenverifier.go", violation)
	if err != nil {
		t.Fatalf("ScanForClaimDerivedTenant: %v", err)
	}

	symbols := map[string]bool{}
	for _, finding := range findings {
		symbols[finding.Symbol] = true
	}
	for _, want := range []string{"TenantMappingMode", "TenantMappingClaim"} {
		if !symbols[want] {
			t.Errorf("the checker missed %s; found %v", want, symbols)
		}
	}
}

// Ordinary tenant-handling code - a TenantID field, a realm string - must
// not be flagged: the checker looks for the specific retired identifiers,
// not for the word "tenant".
func TestTheCheckerAllowsOrdinaryTenantHandling(t *testing.T) {
	const source = `package tokenverifier

type Config struct {
	Realm string
}

type VerifiedToken struct {
	TenantID string
}
`

	findings, err := architecture.ScanForClaimDerivedTenant("libs/tokenverifier/tokenverifier.go", source)
	if err != nil {
		t.Fatalf("ScanForClaimDerivedTenant: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("ordinary tenant handling was flagged: %v", findings)
	}
}
