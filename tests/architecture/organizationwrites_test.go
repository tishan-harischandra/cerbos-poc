package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// The constraint issue #85 asks for explicitly: no write path to
// organizations or memberships exists anywhere in this platform.
func TestNoAdapterWritesAnOrganizationOrMembership(t *testing.T) {
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

		fileFindings, err := architecture.ScanForOrganizationWrite(filepath.ToSlash(relative), string(source))
		if err != nil {
			t.Fatalf("scanning %s: %v", relative, err)
		}
		findings = append(findings, fileFindings...)
	}

	if len(findings) > 0 {
		var report strings.Builder
		report.WriteString("a write path to organizations or memberships was found:\n")
		for _, finding := range findings {
			report.WriteString("  " + finding.String() + "\n")
		}
		t.Fatal(report.String())
	}
}

// An architecture test that cannot fail is decoration.
func TestTheCheckerCatchesAnOrganizationWrite(t *testing.T) {
	const violation = `package keycloak

import "net/http"

func (d *Directory) AddOrganizationMember(ctx context.Context, id string) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, d.adminPath("organizations", id, "members"), nil)
	_, err := d.http.Do(request)
	return err
}
`

	findings, err := architecture.ScanForOrganizationWrite("libs/idpdirectory/keycloak/keycloak.go", violation)
	if err != nil {
		t.Fatalf("ScanForOrganizationWrite: %v", err)
	}
	if len(findings) != 1 || findings[0].Symbol != "AddOrganizationMember: http.MethodPost" {
		t.Errorf("findings = %v, want one for the write in AddOrganizationMember", findings)
	}
}

// A write elsewhere in the same adapter file - minting the service-account
// token, say - must not be mistaken for an organization write.
func TestTheCheckerIgnoresAnUnrelatedWriteInTheSameFile(t *testing.T) {
	const source = `package keycloak

import "net/http"

func (d *Directory) serviceAccountToken(ctx context.Context) (string, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, d.tokenEndpoint(), nil)
	_, err := d.http.Do(request)
	return "", err
}
`

	findings, err := architecture.ScanForOrganizationWrite("libs/idpdirectory/keycloak/keycloak.go", source)
	if err != nil {
		t.Fatalf("ScanForOrganizationWrite: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("an unrelated write was flagged: %v", findings)
	}
}

// The checker only scans the identity directory adapters, not every file in
// the repository.
func TestTheCheckerIgnoresFilesOutsideTheAdapterSurface(t *testing.T) {
	const violation = `package somewhereelse

import "net/http"

func DeleteOrganization(ctx context.Context) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodDelete, "https://example.test/organizations/1", nil)
	_, err := http.DefaultClient.Do(request)
	return err
}
`

	findings, err := architecture.ScanForOrganizationWrite("apps/ads/internal/somewhereelse/somewhereelse.go", violation)
	if err != nil {
		t.Fatalf("ScanForOrganizationWrite: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("a file outside the adapter surface was flagged: %v", findings)
	}
}
