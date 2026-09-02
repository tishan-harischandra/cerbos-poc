package idpdirectory

import "sync"

// Registry holds one identity directory client per tenant, safe for
// concurrent reads and writes (issue #86).
//
// It exists so a tenant onboarded at runtime becomes usable with no
// service restart: Register adds a realm without disturbing any lookup
// already in flight, the same guarantee tokenverifier.Registry already
// gives token verification for exactly this reason.
type Registry struct {
	mu       sync.RWMutex
	byTenant map[TenantID]IdentityDirectory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byTenant: make(map[TenantID]IdentityDirectory)}
}

// Register adds a tenant's directory client. Registering the same tenant
// twice replaces the previous client.
func (r *Registry) Register(tenant TenantID, directory IdentityDirectory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byTenant[tenant] = directory
}

// Get returns the directory client registered for tenant, if any.
func (r *Registry) Get(tenant TenantID) (IdentityDirectory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	directory, ok := r.byTenant[tenant]
	return directory, ok
}

// Known reports every tenant currently registered, in no particular
// order.
func (r *Registry) Known() []TenantID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tenants := make([]TenantID, 0, len(r.byTenant))
	for tenant := range r.byTenant {
		tenants = append(tenants, tenant)
	}
	return tenants
}
