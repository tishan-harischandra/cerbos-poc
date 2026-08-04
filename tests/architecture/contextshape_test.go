package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// The permissionContext the ADS sends to Cerbos carries action sets and a
// revision, and nothing else. A verdict-shaped field on it - "allowed",
// "effect", "decision" - would mean something in Go had already decided, and the
// PDP would be reduced to transporting an answer it did not reach.
func TestThePermissionContextCarriesNoVerdict(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "libs", "permissioncontext", "permissioncontext.go")

	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	findings, err := architecture.ScanPermissionContextShape(
		"libs/permissioncontext/permissioncontext.go", string(source))
	if err != nil {
		t.Fatalf("scanning the permission context: %v", err)
	}

	if len(findings) > 0 {
		var report strings.Builder
		report.WriteString("the permission context has grown a verdict:\n")
		for _, finding := range findings {
			report.WriteString("  " + finding.String() + "\n")
		}
		t.Fatal(report.String())
	}
}

// An architecture test that cannot fail is decoration.
func TestTheCheckerCatchesAVerdictOnTheContext(t *testing.T) {
	const violation = `package permissioncontext

type Context struct {
	RoleGrantedActions []string
	UserGrantedActions []string
	UserRevokedActions []string
	PermissionRevision int64
	Allowed            bool
	DecisionSource     string
}
`

	findings, err := architecture.ScanPermissionContextShape("libs/permissioncontext/permissioncontext.go", violation)
	if err != nil {
		t.Fatalf("ScanPermissionContextShape: %v", err)
	}

	symbols := map[string]bool{}
	for _, finding := range findings {
		symbols[finding.Symbol] = true
	}
	for _, want := range []string{"Allowed", "DecisionSource"} {
		if !symbols[want] {
			t.Errorf("the checker missed %s; found %v", want, symbols)
		}
	}
}

// A field that is neither a verdict nor one of the four is still a finding: the
// context is a contract with the Cerbos resource schema, and a field added on
// one side alone is a field the policies cannot read.
func TestTheCheckerCatchesAnUndeclaredContextField(t *testing.T) {
	const violation = `package permissioncontext

type Context struct {
	RoleGrantedActions []string
	UserGrantedActions []string
	UserRevokedActions []string
	PermissionRevision int64
	HospitalID         string
}
`

	findings, err := architecture.ScanPermissionContextShape("libs/permissioncontext/permissioncontext.go", violation)
	if err != nil {
		t.Fatalf("ScanPermissionContextShape: %v", err)
	}
	if len(findings) != 1 || findings[0].Symbol != "HospitalID" {
		t.Errorf("findings = %v, want one for HospitalID", findings)
	}
}

// The four fields the schema declares must all still be there. Losing one
// silently would leave a policy condition reading an attribute that never
// arrives, which Cerbos answers by denying everything.
func TestTheCheckerCatchesAMissingContextField(t *testing.T) {
	const violation = `package permissioncontext

type Context struct {
	RoleGrantedActions []string
	UserGrantedActions []string
	UserRevokedActions []string
}
`

	findings, err := architecture.ScanPermissionContextShape("libs/permissioncontext/permissioncontext.go", violation)
	if err != nil {
		t.Fatalf("ScanPermissionContextShape: %v", err)
	}
	if len(findings) != 1 || findings[0].Symbol != "PermissionRevision" {
		t.Errorf("findings = %v, want one for the missing PermissionRevision", findings)
	}
}
