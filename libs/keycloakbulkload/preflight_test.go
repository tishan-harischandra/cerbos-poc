package keycloakbulkload_test

import (
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/keycloakbulkload"
)

func TestPreflightRefusesWhenDiskIsInsufficient(t *testing.T) {
	// The full 600,000/42,000,000 population needs gigabytes; /tmp on a
	// nearly-full filesystem would not have to exist for this test to prove
	// the refusal path, but reusing a real, tiny impossible requirement is
	// simplest: ask for petabytes and let the check refuse.
	estimate := keycloakbulkload.PreflightEstimate{
		Users:        1_000_000_000_000,
		RoleMappings: 1_000_000_000_000,
	}
	err := keycloakbulkload.Preflight(estimate, "/tmp")
	if err == nil {
		t.Fatal("Preflight accepted a population that needs more disk than any real host has")
	}
	if !strings.Contains(err.Error(), "disk") && !strings.Contains(err.Error(), "free") {
		t.Errorf("refusal message is not actionable about disk: %v", err)
	}
}

func TestPreflightAcceptsATinyPopulationOnAnOrdinaryHost(t *testing.T) {
	estimate := keycloakbulkload.PreflightEstimate{Users: 50, RoleMappings: 200}
	if err := keycloakbulkload.Preflight(estimate, "/tmp"); err != nil {
		t.Errorf("Preflight refused a tiny population: %v", err)
	}
}

func TestPreflightRefusalNamesThePopulationSize(t *testing.T) {
	estimate := keycloakbulkload.PreflightEstimate{
		Users:        1_000_000_000_000,
		RoleMappings: 1_000_000_000_000,
	}
	err := keycloakbulkload.Preflight(estimate, "/tmp")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "1000000000000") {
		t.Errorf("refusal message does not name the population size: %v", err)
	}
}
