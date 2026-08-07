package policyrelease_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func validReleaseTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "catalog/resources/patient_record.yaml"), "actions: [read, update]")
	writeFile(t, filepath.Join(root, "policies/resources/patient_record.yaml"), "kind: ResourcePolicy")
	writeFile(t, filepath.Join(root, "policies/_schemas/patient_record.json"), `{"type":"object"}`)
	return root
}

func TestValidate_RejectsCatalogResourceWithoutPolicy(t *testing.T) {
	root := validReleaseTree(t)
	writeFile(t, filepath.Join(root, "catalog/resources/orphan.yaml"), "actions: [read]")

	err := policyrelease.Validate(context.Background(), root, policyrelease.ValidateOptions{
		Compiler: &fakeCompiler{},
	})
	if err == nil {
		t.Fatal("Validate: want error for catalog resource without a policy file, got nil")
	}
}

func TestValidate_RejectsPolicyResourceWithoutCatalogEntry(t *testing.T) {
	root := validReleaseTree(t)
	writeFile(t, filepath.Join(root, "policies/resources/orphan.yaml"), "kind: ResourcePolicy")

	err := policyrelease.Validate(context.Background(), root, policyrelease.ValidateOptions{
		Compiler: &fakeCompiler{},
	})
	if err == nil {
		t.Fatal("Validate: want error for policy without a catalog entry (ADR-006), got nil")
	}
}

func TestValidate_RejectsInvalidSchemaJSON(t *testing.T) {
	root := validReleaseTree(t)
	writeFile(t, filepath.Join(root, "policies/_schemas/patient_record.json"), `{not json`)

	err := policyrelease.Validate(context.Background(), root, policyrelease.ValidateOptions{
		Compiler: &fakeCompiler{},
	})
	if err == nil {
		t.Fatal("Validate: want error for invalid schema JSON, got nil")
	}
}

type fakeCompiler struct {
	err        error
	gotDir     string
	gotTestDir string
}

func (f *fakeCompiler) Compile(ctx context.Context, policyDir, testsDir string) error {
	f.gotDir = policyDir
	f.gotTestDir = testsDir
	return f.err
}

func TestValidate_RunsCerbosCompileWithTestsAgainstPoliciesDir(t *testing.T) {
	root := validReleaseTree(t)
	compiler := &fakeCompiler{}

	if err := policyrelease.Validate(context.Background(), root, policyrelease.ValidateOptions{
		Compiler: compiler,
	}); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	wantDir := filepath.Join(root, "policies")
	wantTests := filepath.Join(root, "policies", "tests")
	if compiler.gotDir != wantDir {
		t.Fatalf("compiler policyDir = %q, want %q", compiler.gotDir, wantDir)
	}
	if compiler.gotTestDir != wantTests {
		t.Fatalf("compiler testsDir = %q, want %q", compiler.gotTestDir, wantTests)
	}
}

func TestValidate_PropagatesCompileFailure(t *testing.T) {
	root := validReleaseTree(t)
	wantErr := context.DeadlineExceeded
	err := policyrelease.Validate(context.Background(), root, policyrelease.ValidateOptions{
		Compiler: &fakeCompiler{err: wantErr},
	})
	if err == nil {
		t.Fatal("Validate: want error when cerbos compile fails, got nil")
	}
}
