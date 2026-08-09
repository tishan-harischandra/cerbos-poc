package provider_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/provider"
)

// The one rule that separates this factory from the identity provider one it
// is otherwise modelled on. A wrong IDP_TYPE fails loudly on the first
// request; an unset LEADER_ELECTION_TYPE that quietly defaulted to SINGLE
// would fail silently, as every replica electing itself, which is precisely
// the outage the port exists to prevent.
func TestAnUnsetElectionTypeRefusesToStart(t *testing.T) {
	_, err := provider.FromEnv(lookup(map[string]string{}))
	if !errors.Is(err, leaderlock.ErrNotConfigured) {
		t.Fatalf("FromEnv with no type = %v, want ErrNotConfigured", err)
	}
	if !strings.Contains(err.Error(), provider.EnvType) {
		t.Errorf("error = %v, want it to name %s so an operator knows what to set", err, provider.EnvType)
	}
}

func TestAnUnknownElectionTypeIsRefusedByName(t *testing.T) {
	_, err := provider.FromEnv(lookup(map[string]string{provider.EnvType: "ZOOKEEPER"}))
	if !errors.Is(err, leaderlock.ErrUnknownType) {
		t.Fatalf("FromEnv with an unimplemented type = %v, want ErrUnknownType", err)
	}
	if !strings.Contains(err.Error(), "ZOOKEEPER") {
		t.Errorf("error = %v, want it to name the type nobody implements", err)
	}
}

// The seam is only real if flipping one environment variable changes the
// mechanism with no consumer rebuilt and no code touched.
func TestTheElectorIsSelectedByEnvironmentAlone(t *testing.T) {
	cfg, err := provider.FromEnv(lookup(map[string]string{provider.EnvType: "SINGLE"}))
	if err != nil {
		t.Fatalf("FromEnv(SINGLE): %v", err)
	}
	elector, err := provider.New(cfg)
	if err != nil {
		t.Fatalf("New(SINGLE): %v", err)
	}
	if elector == nil {
		t.Fatal("New returned no elector")
	}
}

// The database-backed types need somewhere to run the election, and an
// installation that already configured the authorization database should not
// have to say so twice.
func TestADatabaseBackedElectionFallsBackToTheAuthorizationDatabase(t *testing.T) {
	cfg, err := provider.FromEnv(lookup(map[string]string{
		provider.EnvType:               "PG_ADVISORY",
		"ASSIGNMENTSTORE_POSTGRES_DSN": "postgres://authz@postgres:5432/authz",
	}))
	if err != nil {
		t.Fatalf("FromEnv(PG_ADVISORY): %v", err)
	}
	if cfg.DSN != "postgres://authz@postgres:5432/authz" {
		t.Errorf("DSN = %q, want the authorization database's own DSN", cfg.DSN)
	}
	if _, err := provider.New(cfg); err != nil {
		t.Errorf("New(PG_ADVISORY): %v", err)
	}
}

func TestADatabaseBackedElectionWithNoDatabaseIsRefused(t *testing.T) {
	cfg, err := provider.FromEnv(lookup(map[string]string{provider.EnvType: "PG_ADVISORY"}))
	if err != nil {
		t.Fatalf("FromEnv(PG_ADVISORY): %v", err)
	}
	if _, err := provider.New(cfg); err == nil {
		t.Error("New built a database-backed elector with nowhere to connect")
	}
}

// A renewal that is not comfortably inside the ttl loses the lease it was
// meant to keep, and the failure looks like random leadership churn rather
// than a misconfiguration.
func TestARenewalSlowerThanTheTTLIsRefused(t *testing.T) {
	_, err := provider.FromEnv(lookup(map[string]string{
		provider.EnvType:          "SINGLE",
		provider.EnvTTL:           "5s",
		provider.EnvRenewInterval: "10s",
	}))
	if err == nil {
		t.Fatal("FromEnv accepted a renewal interval longer than the lease it renews")
	}
}

func lookup(values map[string]string) provider.LookupFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
