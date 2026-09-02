package loadmodel_test

import (
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/loadmodel"
)

var seededAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestDemoConfigAndFullLoadConfigAreBothValid(t *testing.T) {
	for name, cfg := range map[string]loadmodel.Config{
		"demo": loadmodel.DemoConfig(),
		"load": loadmodel.FullLoadConfig(),
	} {
		if err := cfg.Validate(); err != nil {
			t.Errorf("%s config: %v", name, err)
		}
	}
}

func TestFullLoadConfigMatchesTheDocumentedLoadModel(t *testing.T) {
	cfg := loadmodel.FullLoadConfig()
	if cfg.Tenants != 5 {
		t.Errorf("Tenants = %d, want 5", cfg.Tenants)
	}
	if cfg.Tenants*cfg.HospitalsPerTenant != 20 {
		t.Errorf("total hospitals = %d, want 20", cfg.Tenants*cfg.HospitalsPerTenant)
	}
	if cfg.CanonicalRoles != 250 {
		t.Errorf("CanonicalRoles = %d, want 250", cfg.CanonicalRoles)
	}
	if cfg.Users != 600_000 {
		t.Errorf("Users = %d, want 600000", cfg.Users)
	}
	if cfg.RolesPerUser != 70 {
		t.Errorf("RolesPerUser = %d, want 70", cfg.RolesPerUser)
	}
	if got, want := cfg.Users*cfg.RolesPerUser, 42_000_000; got != want {
		t.Errorf("total role mappings = %d, want %d", got, want)
	}
}

func TestGeneratingThePopulationTwiceProducesIdenticalUsers(t *testing.T) {
	cfg := loadmodel.DemoConfig()
	a, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < cfg.Users; i++ {
		ua, ub := a.User(i), b.User(i)
		if ua.Username != ub.Username || ua.TenantID != ub.TenantID ||
			len(ua.HospitalIDs) != len(ub.HospitalIDs) || len(ua.RoleNames) != len(ub.RoleNames) {
			t.Fatalf("user %d differs between runs: %+v vs %+v", i, ua, ub)
		}
		for j := range ua.HospitalIDs {
			if ua.HospitalIDs[j] != ub.HospitalIDs[j] {
				t.Fatalf("user %d hospital %d differs: %q vs %q", i, j, ua.HospitalIDs[j], ub.HospitalIDs[j])
			}
		}
		for j := range ua.RoleNames {
			if ua.RoleNames[j] != ub.RoleNames[j] {
				t.Fatalf("user %d role %d differs: %q vs %q", i, j, ua.RoleNames[j], ub.RoleNames[j])
			}
		}
	}
}

func TestEveryUserHasExactlyRolesPerUserDistinctRoles(t *testing.T) {
	cfg := loadmodel.FullLoadConfig()
	cfg.Users = 1000 // exercising the full 600,000 population is the load run itself, not this test
	pop, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < cfg.Users; i++ {
		user := pop.User(i)
		if len(user.RoleNames) != cfg.RolesPerUser {
			t.Fatalf("user %d has %d roles, want %d", i, len(user.RoleNames), cfg.RolesPerUser)
		}
		seen := make(map[string]bool, len(user.RoleNames))
		for _, role := range user.RoleNames {
			if seen[role] {
				t.Fatalf("user %d has a duplicate role %q", i, role)
			}
			seen[role] = true
		}
	}
}

// issue #87: multi-hospital membership is the common case, not an edge
// case - every user belongs to HospitalsPerUser distinct hospitals.
func TestEveryUserBelongsToHospitalsPerUserDistinctHospitals(t *testing.T) {
	cfg := loadmodel.FullLoadConfig()
	cfg.Users = 1000
	pop, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < cfg.Users; i++ {
		user := pop.User(i)
		if len(user.HospitalIDs) != cfg.HospitalsPerUser {
			t.Fatalf("user %d belongs to %d hospitals, want %d", i, len(user.HospitalIDs), cfg.HospitalsPerUser)
		}
		seen := make(map[string]bool, len(user.HospitalIDs))
		for _, hospital := range user.HospitalIDs {
			if seen[hospital] {
				t.Fatalf("user %d has a duplicate hospital membership %q", i, hospital)
			}
			seen[hospital] = true
		}
		if user.HospitalID() != user.HospitalIDs[0] {
			t.Errorf("user %d: HospitalID() = %q, want HospitalIDs[0] = %q", i, user.HospitalID(), user.HospitalIDs[0])
		}
	}
}

func TestUsersAreDistributedAcrossEveryTenantAndHospital(t *testing.T) {
	cfg := loadmodel.DemoConfig()
	cfg.Users = 400
	pop, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	seenHospital := make(map[string]bool)
	for i := 0; i < cfg.Users; i++ {
		user := pop.User(i)
		for _, hospital := range user.HospitalIDs {
			seenHospital[hospital] = true
		}
	}

	for _, tenant := range pop.TenantIDs() {
		for _, hospital := range pop.HospitalIDs(tenant) {
			if !seenHospital[hospital] {
				t.Errorf("hospital %s never received a user", hospital)
			}
		}
	}
}

func TestHasOverridesSelectsAboutOneInOverrideEveryNthUser(t *testing.T) {
	cfg := loadmodel.DemoConfig()
	pop, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	count := 0
	for i := 0; i < cfg.Users; i++ {
		if pop.HasOverrides(i) {
			count++
		}
	}
	want := cfg.Users / cfg.OverrideEveryNthUser
	if count != want {
		t.Errorf("override users = %d, want %d", count, want)
	}
}

func TestOverridesMixGrantRevokeAndExpired(t *testing.T) {
	cfg := loadmodel.DemoConfig()
	pop, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var sawGrant, sawRevoke, sawExpired bool
	for i := 0; i < cfg.Users; i++ {
		for _, override := range pop.Overrides(i, seededAt) {
			switch {
			case override.Effect == assignmentstore.EffectRevoke:
				sawRevoke = true
			case override.Effect == assignmentstore.EffectGrant && override.ValidUntil.Before(seededAt):
				sawExpired = true
			case override.Effect == assignmentstore.EffectGrant:
				sawGrant = true
			}
		}
	}
	if !sawGrant || !sawRevoke || !sawExpired {
		t.Errorf("override mix incomplete: grant=%v revoke=%v expired=%v", sawGrant, sawRevoke, sawExpired)
	}
}

func TestOverridesAreAllDistinctPerUserEvenBeyondTheActionCount(t *testing.T) {
	cfg := loadmodel.DemoConfig()
	cfg.OverrideEveryNthUser = 1 // every user carries overrides, so rank sweeps 0..9 within the first 10 users
	pop, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("building the population: %v", err)
	}

	for i := 0; i < 10 && i < cfg.Users; i++ {
		overrides := pop.Overrides(i, seededAt)
		want := 1 + i%10
		if len(overrides) != want {
			t.Fatalf("user %d: got %d overrides, want %d", i, len(overrides), want)
		}
		seen := make(map[assignmentstore.UserOverrideKey]bool, len(overrides))
		for _, o := range overrides {
			if seen[o.Key] {
				t.Errorf("user %d: duplicate override key %+v - two overrides collided onto one row", i, o.Key)
			}
			seen[o.Key] = true
		}
		if len(seen) != want {
			t.Errorf("user %d: %d distinct override keys, want %d", i, len(seen), want)
		}
	}
}

func TestOverridesIsNilForUsersWithoutOverrides(t *testing.T) {
	cfg := loadmodel.DemoConfig()
	pop, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < cfg.Users; i++ {
		if pop.HasOverrides(i) {
			continue
		}
		if got := pop.Overrides(i, seededAt); got != nil {
			t.Fatalf("user %d has no overrides per HasOverrides but Overrides returned %d rows", i, len(got))
		}
	}
}

func TestResourcesIncludeLockedInstancesAcrossEveryConfiguredType(t *testing.T) {
	cfg := loadmodel.DemoConfig()
	pop, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	lockedByType := make(map[string]int)
	activeByType := make(map[string]int)
	for _, resource := range pop.Resources(seededAt) {
		switch resource.Status {
		case "LOCKED":
			lockedByType[resource.ResourceType]++
		case "ACTIVE":
			activeByType[resource.ResourceType]++
		}
	}
	for _, resourceType := range cfg.ResourceTypes {
		if lockedByType[resourceType] == 0 {
			t.Errorf("resource type %q has no LOCKED instance", resourceType)
		}
		if activeByType[resourceType] == 0 {
			t.Errorf("resource type %q has no ACTIVE instance", resourceType)
		}
	}
}

func TestRolePermissionsCoverEveryTenantRoleAndAction(t *testing.T) {
	cfg := loadmodel.DemoConfig()
	pop, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	permissions := pop.RolePermissions(seededAt)
	want := cfg.Tenants * cfg.CanonicalRoles * len(loadmodel.Actions)
	if len(permissions) != want {
		t.Fatalf("role_permission rows = %d, want %d", len(permissions), want)
	}
	for _, permission := range permissions {
		if !permission.Enabled {
			t.Errorf("%+v is disabled; the load model grants every role every action", permission.Key)
		}
	}
}

func TestUsersStreamsExactlyUsersCountRecords(t *testing.T) {
	cfg := loadmodel.DemoConfig()
	pop, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	count := 0
	pop.Users(func(loadmodel.User) bool {
		count++
		return true
	})
	if count != cfg.Users {
		t.Errorf("streamed %d users, want %d", count, cfg.Users)
	}
}

func TestUsersStreamStopsWhenTheCallerReturnsFalse(t *testing.T) {
	cfg := loadmodel.DemoConfig()
	pop, err := loadmodel.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	count := 0
	pop.Users(func(loadmodel.User) bool {
		count++
		return count < 3
	})
	if count != 3 {
		t.Errorf("streamed %d users after the caller asked to stop, want 3", count)
	}
}

func TestValidateRejectsAnIncoherentConfig(t *testing.T) {
	cases := []loadmodel.Config{
		{Tenants: 0, HospitalsPerTenant: 1, CanonicalRoles: 1, RolesPerUser: 1, OverrideEveryNthUser: 1},
		{Tenants: 1, HospitalsPerTenant: 0, CanonicalRoles: 1, RolesPerUser: 1, OverrideEveryNthUser: 1},
		{Tenants: 1, HospitalsPerTenant: 1, CanonicalRoles: 0, RolesPerUser: 1, OverrideEveryNthUser: 1},
		{Tenants: 1, HospitalsPerTenant: 1, CanonicalRoles: 5, RolesPerUser: 10, OverrideEveryNthUser: 1},
		{Tenants: 1, HospitalsPerTenant: 1, CanonicalRoles: 5, RolesPerUser: 1, OverrideEveryNthUser: 0},
		{Tenants: 1, HospitalsPerTenant: 4, CanonicalRoles: 5, RolesPerUser: 1, OverrideEveryNthUser: 1, HospitalsPerUser: 0},
		{Tenants: 1, HospitalsPerTenant: 4, CanonicalRoles: 5, RolesPerUser: 1, OverrideEveryNthUser: 1, HospitalsPerUser: 5},
	}
	for i, cfg := range cases {
		if err := cfg.Validate(); err == nil {
			t.Errorf("case %d: Validate accepted an incoherent config: %+v", i, cfg)
		}
	}
}
