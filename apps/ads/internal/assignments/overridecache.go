package assignments

import (
	"context"
	"sync"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/libs/permissioncontext"
)

// CachingOverrides is the read-through cache in front of a user-override
// source (§11.2, §17.1's "ADS cache hit ratios for role permissions and
// user overrides"). It mirrors CachingRoleMatrix's TTL and never-cache-a-
// failure discipline, over the Overrides port instead of RoleMatrix: the
// two are resolved from different places and change at different times,
// which is why Resolver already keeps them as separate collaborators.
type CachingOverrides struct {
	inner Overrides
	ttl   time.Duration
	now   func() time.Time

	mu      sync.Mutex
	entries map[overrideCacheKey]cachedOverrides
}

// OverrideCacheConfig holds the cache's collaborators.
type OverrideCacheConfig struct {
	Overrides Overrides
	// TTL bounds how long an entry may outlive a change to the underlying
	// row. Non-positive falls back to DefaultCacheTTL.
	TTL time.Duration
	// Now defaults to time.Now.
	Now func() time.Time
}

type overrideCacheKey struct {
	tenantID   string
	hospitalID string
	userID     string
	resource   string
	resourceID string
}

type cachedOverrides struct {
	overrides []permissioncontext.UserOverride
	readAt    time.Time
}

// NewCachingOverrides wraps an Overrides source in a read-through cache.
func NewCachingOverrides(cfg OverrideCacheConfig) *CachingOverrides {
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &CachingOverrides{
		inner:   cfg.Overrides,
		ttl:     ttl,
		now:     now,
		entries: make(map[overrideCacheKey]cachedOverrides),
	}
}

func keyOf(query authz.AssignmentQuery) overrideCacheKey {
	return overrideCacheKey{
		tenantID:   query.TenantID,
		hospitalID: query.HospitalID,
		userID:     query.PrincipalID,
		resource:   query.ResourceKind,
		resourceID: query.ResourceID,
	}
}

// For implements Overrides, serving from memory when the entry is fresh
// and reading through otherwise.
func (c *CachingOverrides) For(ctx context.Context, query authz.AssignmentQuery) ([]permissioncontext.UserOverride, error) {
	now := c.now()
	key := keyOf(query)

	c.mu.Lock()
	entry, cached := c.entries[key]
	c.mu.Unlock()
	if cached && now.Sub(entry.readAt) < c.ttl {
		return entry.overrides, nil
	}

	fresh, err := c.inner.For(ctx, query)
	if err != nil {
		// Deliberately nothing is stored: caching a transient failure as
		// "no overrides" would turn an outage into a lasting incorrect
		// answer, the same discipline CachingRoleMatrix applies.
		return nil, err
	}

	c.mu.Lock()
	c.entries[key] = cachedOverrides{overrides: fresh, readAt: now}
	c.mu.Unlock()

	return fresh, nil
}

// InvalidateUser drops every cached entry for one user within one tenant,
// across every resource it was cached under - the outbox consumer's
// SUBJECT_USER events (§10.1) act on this.
func (c *CachingOverrides) InvalidateUser(tenantID, userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if key.tenantID == tenantID && key.userID == userID {
			delete(c.entries, key)
		}
	}
}

// InvalidateTenant drops every cached entry for one tenant, the same
// backstop CachingRoleMatrix's InvalidateTenant gives the reconciler.
func (c *CachingOverrides) InvalidateTenant(tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if key.tenantID == tenantID {
			delete(c.entries, key)
		}
	}
}
