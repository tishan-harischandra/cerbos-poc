package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/tests/architecture"
)

// The constraint: every provider-neutral port in this repository is a library
// behind dependency inversion, so no consumer may name a concrete adapter. If
// one did, "set IDP_TYPE=WSO2_IS and rebuild nothing" - or
// "LEADER_ELECTION_TYPE=K8S_LEASE" - would stop being true the moment that
// consumer needed a product-shaped type.
func TestNoConsumerImportsAConcreteAdapter(t *testing.T) {
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
		report.WriteString("a concrete adapter is imported outside its provider factory:\n")
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

// The same rule, the second port. A service that named an elector could not be
// moved from compose to Kubernetes without a code change (ADR-009), which is
// the entire reason leaderlock exists.
func TestTheCheckerCatchesAServiceReachingForAnElector(t *testing.T) {
	const violation = `package main

import (
	"context"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/pgadvisory"
)

// The defect: a service that has to know how the cluster coordinates.
func elect(ctx context.Context, elector *pgadvisory.Elector) error {
	return elector.Run(ctx, "outbox-publisher", func(context.Context) {})
}
`

	findings, err := architecture.ScanAdapterImports(
		"apps/admin-service/cmd/admin-service/main.go",
		"apps/admin-service/cmd/admin-service/main.go",
		violation)
	if err != nil {
		t.Fatalf("ScanAdapterImports: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1; got %v", len(findings), findings)
	}
	if !strings.HasSuffix(findings[0].Symbol, "/pgadvisory") {
		t.Errorf("finding names %q, want the advisory lock adapter", findings[0].Symbol)
	}
}

// Every adapter the factory can select must be listed, or the rule silently
// stops covering whichever one was added last.
func TestEveryLeaderElectionAdapterIsGuarded(t *testing.T) {
	guarded := map[string]bool{}
	for _, adapter := range architecture.ConcreteAdapterPackages {
		guarded[adapter] = true
	}
	for _, adapter := range []string{"pgadvisory", "databaselock", "k8slease", "redislock", "single"} {
		path := "github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/" + adapter
		if !guarded[path] {
			t.Errorf("%s is selectable by LEADER_ELECTION_TYPE but no rule stops a consumer naming it", path)
		}
		if !architecture.IsCompositionRoot("libs/leaderlock/" + adapter + "/x.go") {
			t.Errorf("%s cannot import itself, so its own package would fail the rule", path)
		}
	}
	if !architecture.IsCompositionRoot("libs/leaderlock/provider/provider.go") {
		t.Error("the leader election factory is not a composition root, so it cannot name the adapters it selects")
	}
}

func TestCompositionRootRules(t *testing.T) {
	cases := map[string]bool{
		"libs/idpdirectory/provider/provider.go":   true,
		"libs/idpdirectory/keycloak/keycloak.go":   true,
		"libs/idpdirectory/wso2/wso2.go":           true,
		"libs/leaderlock/provider/provider.go":     true,
		"libs/leaderlock/k8slease/k8slease.go":     true,
		"libs/leaderlock/port.go":                  false,
		"libs/leaderlock/lease/lease.go":           false,
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
