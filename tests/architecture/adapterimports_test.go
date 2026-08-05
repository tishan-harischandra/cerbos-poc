package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// The constraint: the identity provider adapter is a library behind dependency
// inversion, so no consumer may name a concrete adapter. If one did, "set
// IDP_TYPE=WSO2_IS and rebuild nothing" would stop being true the moment that
// consumer needed a Keycloak-shaped type.
func TestNoConsumerImportsAConcreteIdentityProviderAdapter(t *testing.T) {
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

		fileFindings, err := architecture.ScanAdapterImports(relative, relative, string(source))
		if err != nil {
			t.Fatalf("scanning %s: %v", relative, err)
		}
		findings = append(findings, fileFindings...)
	}

	if len(findings) > 0 {
		var report strings.Builder
		report.WriteString("a concrete identity provider adapter is imported outside the provider factory:\n")
		for _, finding := range findings {
			report.WriteString("  " + finding.String() + "\n")
		}
		t.Fatal(report.String())
	}
}

// An architecture test that cannot fail is decoration.
func TestTheCheckerCatchesAConsumerReachingForAnAdapter(t *testing.T) {
	const violation = `package assignments

import (
	"context"

	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/keycloak"
)

// The defect: a consumer that has to know which product is installed.
func lookup(ctx context.Context, directory *keycloak.Directory) error {
	_, err := directory.GetUser(ctx, "tenant-a", "user-doctor")
	return err
}
`

	findings, err := architecture.ScanAdapterImports(
		"apps/ads/internal/assignments/lookup.go",
		"apps/ads/internal/assignments/lookup.go",
		violation)
	if err != nil {
		t.Fatalf("ScanAdapterImports: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1; got %v", len(findings), findings)
	}
	if !strings.HasSuffix(findings[0].Symbol, "/keycloak") {
		t.Errorf("finding names %q, want the Keycloak adapter", findings[0].Symbol)
	}
}

func TestTheProviderFactoryMayNameEveryAdapter(t *testing.T) {
	const factory = `package provider

import (
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/keycloak"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/wso2"
)

var _ = keycloak.New
var _ = wso2.New
`

	findings, err := architecture.ScanAdapterImports(
		"libs/idpdirectory/provider/provider.go",
		"libs/idpdirectory/provider/provider.go",
		factory)
	if err != nil {
		t.Fatalf("ScanAdapterImports: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("the composition root was flagged: %v", findings)
	}
}

func TestCompositionRootRules(t *testing.T) {
	cases := map[string]bool{
		"libs/idpdirectory/provider/provider.go":   true,
		"libs/idpdirectory/keycloak/keycloak.go":   true,
		"libs/idpdirectory/wso2/wso2.go":           true,
		"apps/ads/internal/authz/authz_test.go":    true,
		"apps/ads/cmd/ads/main.go":                 false,
		"apps/ads/internal/tokenauth/tokenauth.go": false,
		"libs/idpdirectory/port.go":                false,
	}

	for path, want := range cases {
		if got := architecture.IsCompositionRoot(path); got != want {
			t.Errorf("IsCompositionRoot(%q) = %t, want %t", path, got, want)
		}
	}
}
