package policyrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Compiler runs cerbos compile (and the policy test suite through its
// --tests flag) against an extracted release tree. The real implementation
// shells out to the cerbos binary (§13.2 steps 17-18: "run cerbos compile and
// all policy tests", "run generated invariants for tenant and hospital
// isolation and permission precedence" - the generated invariants are part of
// the same committed test suite cataloggen emits, so one compile-with-tests
// run covers both).
type Compiler interface {
	Compile(ctx context.Context, policyDir, testsDir string) error
}

// ValidateOptions configures Validate.
type ValidateOptions struct {
	Compiler Compiler
}

// Validate runs the full validation gate (§13.2) against an extracted release
// tree rooted at root. The tree is expected to hold "catalog/resources/*"
// (the authorization catalog) and "policies/resources/*", "policies/_schemas/*"
// and "policies/tests/*" (the Cerbos policy bundle), matching the layout this
// repository already commits under deploy/cerbos.
//
// A tag that fails any of these checks never activates: Validate returns
// before any archive is built, so nothing about the currently served
// revision changes.
func Validate(ctx context.Context, root string, opts ValidateOptions) error {
	if err := validateCatalogConsistency(root); err != nil {
		return err
	}
	if err := validateSchemas(root); err != nil {
		return err
	}
	if opts.Compiler == nil {
		return fmt.Errorf("policyrelease: no Compiler configured")
	}
	policyDir := filepath.Join(root, "policies")
	testsDir := filepath.Join(policyDir, "tests")
	if err := opts.Compiler.Compile(ctx, policyDir, testsDir); err != nil {
		return fmt.Errorf("policyrelease: cerbos compile failed: %w", err)
	}
	return nil
}

// validateCatalogConsistency enforces ADR-006: exactly one resource policy
// file per catalog resource, in both directions. A catalog entry with no
// policy would serve a resource nothing can decide on; a policy with no
// catalog entry is dead weight nothing else in the system knows about.
func validateCatalogConsistency(root string) error {
	catalogNames, err := yamlBaseNames(filepath.Join(root, "catalog", "resources"))
	if err != nil {
		return fmt.Errorf("policyrelease: reading catalog resources: %w", err)
	}
	policyNames, err := yamlBaseNames(filepath.Join(root, "policies", "resources"))
	if err != nil {
		return fmt.Errorf("policyrelease: reading resource policies: %w", err)
	}

	for name := range catalogNames {
		if !policyNames[name] {
			return fmt.Errorf("policyrelease: catalog resource %q has no resource policy (ADR-006)", name)
		}
	}
	for name := range policyNames {
		if !catalogNames[name] {
			return fmt.Errorf("policyrelease: resource policy %q has no catalog entry (ADR-006)", name)
		}
	}
	return nil
}

func yamlBaseNames(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		names[strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".yaml"), ".yml")] = true
	}
	return names, nil
}

// validateSchemas rejects any schema file that is not well-formed JSON before
// cerbos compile is asked to trust it.
func validateSchemas(root string) error {
	dir := filepath.Join(root, "policies", "_schemas")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("policyrelease: reading schemas: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("policyrelease: reading schema %s: %w", e.Name(), err)
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("policyrelease: schema %s is not valid JSON: %w", e.Name(), err)
		}
	}
	return nil
}
