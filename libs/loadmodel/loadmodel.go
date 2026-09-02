// Package loadmodel generates the §15 load population - tenants, hospitals,
// canonical roles, users with role assignments, user overrides and FHIR
// resource instances - from a Config's counts alone.
//
// The demo profile and the load profile are the same generator at different
// Config values, never a different code path (§15's "Load and demo differ by
// configuration only"): DemoConfig and FullLoadConfig below are both plain
// Config values, and every caller - the load-test harness and any demo
// tooling that wants a bigger fixture than demoseed's handful of named users
// - goes through the same Population methods.
//
// Every field of every generated record is a pure function of its index, not
// of a pseudo-random generator: two Populations built from the same Config
// are byte-identical (§24's determinism criterion) by construction, and any
// single user's row can be recomputed and audited without generating the
// population around it.
//
// The generated role-permission and override rows use one reference resource
// (ResourceKey) rather than the full ~157-type FHIR catalog: the load model's
// point is exercising authorization at population scale, and the catalog's
// breadth is already covered by the policy test suite and libs/cataloggen.
// FHIR resource *instances* (Resources) do span multiple resource types,
// because issue #24's acceptance criteria ask for that explicitly.
package loadmodel

import (
	"fmt"
)

// ResourceKey is the reference resource role_permission and user overrides
// are generated against, matching demoseed's own choice so a load-scale
// tenant's matrix is shaped like the demo tenant's, just bigger.
const ResourceKey = "patient_record"

// Actions are the actions generated against ResourceKey.
var Actions = []string{"read", "update", "delete"}

// Config sizes one population. Every field mirrors a dimension of the §15
// load model table.
type Config struct {
	Tenants            int
	HospitalsPerTenant int
	CanonicalRoles     int
	Users              int
	RolesPerUser       int
	// OverrideEveryNthUser selects which users carry overrides: user index i
	// carries overrides when i % OverrideEveryNthUser == 0. 20 means ~5%.
	OverrideEveryNthUser int
	// HospitalsPerUser is how many of a tenant's hospitals - Keycloak
	// organizations (issue #87) - each user is a member of. 2 makes
	// multi-hospital membership the common case in the data rather than an
	// edge case, per the §15 load model. Must be at least 1 and at most
	// HospitalsPerTenant.
	HospitalsPerUser int
	// ResourceTypes are the FHIR resource types Resources() generates
	// instances for. ResourceKey's own type is included automatically.
	ResourceTypes []string
	// ActiveInstancesPerResourceType and LockedInstancesPerResourceType are
	// generated per tenant/hospital pair, for every resource type.
	ActiveInstancesPerResourceType int
	LockedInstancesPerResourceType int
}

// DemoConfig is a small population: the same generator as FullLoadConfig,
// sized for a developer's laptop and for tests that assert on generated
// output.
func DemoConfig() Config {
	return Config{
		Tenants:                        2,
		HospitalsPerTenant:             2,
		CanonicalRoles:                 10,
		Users:                          50,
		RolesPerUser:                   4,
		OverrideEveryNthUser:           10,
		HospitalsPerUser:               2,
		ResourceTypes:                  []string{"condition"},
		ActiveInstancesPerResourceType: 2,
		LockedInstancesPerResourceType: 1,
	}
}

// FullLoadConfig is the exact §15 load model: 5 tenants, 20 hospitals (4 per
// tenant), 250 canonical roles, 600,000 users, 70 roles per user (42,000,000
// mappings), overrides on ~5% of users, every user a member of 2 of its
// tenant's 4 hospitals (issue #87).
func FullLoadConfig() Config {
	return Config{
		Tenants:                        5,
		HospitalsPerTenant:             4,
		CanonicalRoles:                 250,
		Users:                          600_000,
		RolesPerUser:                   70,
		OverrideEveryNthUser:           20,
		HospitalsPerUser:               2,
		ResourceTypes:                  []string{"condition", "observation", "medication_request"},
		ActiveInstancesPerResourceType: 20,
		LockedInstancesPerResourceType: 5,
	}
}

// Validate rejects a Config the generator cannot honour.
func (c Config) Validate() error {
	switch {
	case c.Tenants <= 0:
		return fmt.Errorf("loadmodel: Tenants must be positive")
	case c.HospitalsPerTenant <= 0:
		return fmt.Errorf("loadmodel: HospitalsPerTenant must be positive")
	case c.CanonicalRoles <= 0:
		return fmt.Errorf("loadmodel: CanonicalRoles must be positive")
	case c.Users < 0:
		return fmt.Errorf("loadmodel: Users must not be negative")
	case c.RolesPerUser <= 0:
		return fmt.Errorf("loadmodel: RolesPerUser must be positive")
	case c.RolesPerUser > c.CanonicalRoles:
		return fmt.Errorf("loadmodel: RolesPerUser (%d) exceeds CanonicalRoles (%d)", c.RolesPerUser, c.CanonicalRoles)
	case c.OverrideEveryNthUser <= 0:
		return fmt.Errorf("loadmodel: OverrideEveryNthUser must be positive")
	case c.HospitalsPerUser <= 0:
		return fmt.Errorf("loadmodel: HospitalsPerUser must be positive")
	case c.HospitalsPerUser > c.HospitalsPerTenant:
		return fmt.Errorf("loadmodel: HospitalsPerUser (%d) exceeds HospitalsPerTenant (%d)", c.HospitalsPerUser, c.HospitalsPerTenant)
	}
	if c.RolesPerUser > 1 {
		// roleStep must be coprime with CanonicalRoles for User's role
		// selection to produce RolesPerUser *distinct* roles (see User
		// below); roleStep is fixed at 7, so this rejects configurations
		// where that guarantee would not hold.
		if gcd(roleStep, c.CanonicalRoles) != 1 {
			return fmt.Errorf("loadmodel: CanonicalRoles (%d) must be coprime with %d for distinct role assignment",
				c.CanonicalRoles, roleStep)
		}
	}
	return nil
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Population is one generated load model. Every accessor is a pure function
// of Config and an index; nothing here is mutable generator state, so
// concurrent readers and repeated calls are safe and free.
type Population struct {
	cfg       Config
	tenantIDs []string
	hospitals map[string][]string
	roleNames []string
}

// New validates cfg and returns the population it describes.
func New(cfg Config) (*Population, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	p := &Population{cfg: cfg, hospitals: make(map[string][]string, cfg.Tenants)}
	for t := 0; t < cfg.Tenants; t++ {
		tenantID := fmt.Sprintf("tenant-%d", t+1)
		p.tenantIDs = append(p.tenantIDs, tenantID)
		hospitals := make([]string, cfg.HospitalsPerTenant)
		for h := 0; h < cfg.HospitalsPerTenant; h++ {
			hospitals[h] = fmt.Sprintf("%s-hospital-%d", tenantID, h+1)
		}
		p.hospitals[tenantID] = hospitals
	}
	p.roleNames = make([]string, cfg.CanonicalRoles)
	for r := 0; r < cfg.CanonicalRoles; r++ {
		p.roleNames[r] = fmt.Sprintf("load-role-%03d", r)
	}
	return p, nil
}

// Config returns the configuration this population was built from.
func (p *Population) Config() Config { return p.cfg }

// TenantIDs returns every tenant, in generation order.
func (p *Population) TenantIDs() []string { return append([]string(nil), p.tenantIDs...) }

// HospitalIDs returns tenantID's hospitals.
func (p *Population) HospitalIDs(tenantID string) []string {
	return append([]string(nil), p.hospitals[tenantID]...)
}

// RoleNames returns every canonical role in the catalog.
func (p *Population) RoleNames() []string { return append([]string(nil), p.roleNames...) }

// roleStep is the fixed stride User uses to pick RolesPerUser roles out of
// CanonicalRoles. See Config.Validate's coprimality check.
const roleStep = 7

// User is one generated identity. Fields mirror the population's dimensions:
// TenantID and HospitalIDs place the user, RoleNames are exactly
// RolesPerUser distinct canonical roles.
type User struct {
	Index     int
	Username  string
	FirstName string
	LastName  string
	Email     string
	TenantID  string
	// HospitalIDs are exactly HospitalsPerUser distinct hospitals - Keycloak
	// organizations (issue #87) - this user belongs to. HospitalIDs[0] is
	// this user's primary hospital: the one a generated override or
	// resource-instance grant targets, and what HospitalID mirrors for a
	// caller that only ever cared about one.
	HospitalIDs []string
	RoleNames   []string
}

// HospitalID is this user's primary hospital - HospitalIDs[0] - kept as its
// own accessor so a caller from before issue #87 (a single hospital per
// user) does not need to know the membership set exists.
func (u User) HospitalID() string { return u.HospitalIDs[0] }

// User computes the index-th user. Deterministic: calling it twice with the
// same index on populations built from the same Config returns identical
// values.
func (p *Population) User(index int) User {
	tenant := p.tenantIDs[index%len(p.tenantIDs)]
	hospitalsForTenant := p.hospitals[tenant]

	// HospitalsPerUser distinct hospitals, starting at the same
	// deterministic offset single-membership users always used and
	// stepping forward by one hospital per membership - distinct as long
	// as HospitalsPerUser <= HospitalsPerTenant (Config.Validate's own
	// check).
	base := (index / len(p.tenantIDs)) % len(hospitalsForTenant)
	hospitalIDs := make([]string, p.cfg.HospitalsPerUser)
	for k := range hospitalIDs {
		hospitalIDs[k] = hospitalsForTenant[(base+k)%len(hospitalsForTenant)]
	}

	roles := make([]string, p.cfg.RolesPerUser)
	for j := range roles {
		roles[j] = p.roleNames[(index+j*roleStep)%len(p.roleNames)]
	}

	username := fmt.Sprintf("load-user-%07d", index)
	return User{
		Index:       index,
		Username:    username,
		FirstName:   "Load",
		LastName:    fmt.Sprintf("User%07d", index),
		Email:       username + "@example.test",
		TenantID:    tenant,
		HospitalIDs: hospitalIDs,
		RoleNames:   roles,
	}
}

// Users streams every generated user in index order. A caller feeding
// BulkLoad's channel-shaped API never needs Users materialised as a slice.
func (p *Population) Users(yield func(User) bool) {
	for i := 0; i < p.cfg.Users; i++ {
		if !yield(p.User(i)) {
			return
		}
	}
}

// HasOverrides reports whether the index-th user is one of the ~5% (per
// OverrideEveryNthUser) carrying user overrides.
func (p *Population) HasOverrides(index int) bool {
	return index%p.cfg.OverrideEveryNthUser == 0
}
