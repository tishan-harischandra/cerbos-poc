package assignments

import (
	"context"
	"sync"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// DefaultCacheTTL bounds how long a cached matrix entry may outlive a change to
// the underlying row.
//
// It is short because it is the only invalidation mechanism there is so far.
// Targeted invalidation driven by the outbox arrives with its own slice, and
// when it does this becomes a backstop rather than the primary mechanism.
const DefaultCacheTTL = 30 * time.Second

// CacheMetrics reports whether a lookup was served from memory or had to
// read through, so §17.1's per-cache hit ratio has something to divide.
type CacheMetrics interface {
	Hit()
	Miss()
}

type noopCacheMetrics struct{}

func (noopCacheMetrics) Hit()  {}
func (noopCacheMetrics) Miss() {}

// CachingRoleMatrix is the in-process warm path in front of the authorization
// database (§11.2).
//
// The cache is keyed by tenant, canonical role and resource. §11.2 keys the role
// permission cache by tenant and role alone; narrowing it by resource is what
// the decision path actually asks for, and it keeps one entry to the size of a
// single resource's action set rather than a whole tenant's matrix for a role.
//
// Entries hold facts only - which actions a role grants and whether the row is
// enabled. Nothing cached here is a verdict, so a stale entry can only be wrong
// about the data, never about the precedence rules.
//
// The TTL is the staleness bound on two different things, and the second is
// easy to miss. It bounds how long an edited row stays unseen, which is what it
// is for. It equally bounds how long a validity window transition stays unseen:
// a grant that expires, or one that becomes valid, is only noticed when its
// entry is next read through. The query still filters on the caller's instant,
// so the window is honoured at read time - it is the cached copy that lags.
//
// Memory is bounded by the live keyspace rather than by traffic: entries are
// keyed by tenant, role and resource and are overwritten in place, so repeated
// decisions reuse keys instead of accumulating them. There is deliberately no
// eviction beyond that. Adding one before the load slice has measured the real
// keyspace would be machinery built against a guess.
type CachingRoleMatrix struct {
	inner   RoleMatrix
	ttl     time.Duration
	metrics CacheMetrics
	// now is the clock entries are aged against, injected so tests can advance
	// time without sleeping. Unexported: a caller mutating it while decisions
	// are in flight would be a data race on the hot path.
	now func() time.Time

	mu        sync.Mutex
	roles     map[roleCacheKey]cachedActions
	revisions map[string]cachedRevision
}

// CacheConfig holds the cache's collaborators.
type CacheConfig struct {
	Matrix RoleMatrix
	// TTL bounds how long an entry may outlive a change to the underlying row.
	// Non-positive falls back to DefaultCacheTTL.
	TTL time.Duration
	// Now defaults to time.Now.
	Now func() time.Time
	// Metrics reports hits and misses (§17.1). Nil means no reporting.
	Metrics CacheMetrics
}

type roleCacheKey struct {
	tenantID    string
	roleID      string
	resourceKey string
}

type cachedActions struct {
	permissions []assignmentstore.RolePermission
	readAt      time.Time
}

type cachedRevision struct {
	revision assignmentstore.PermissionRevision
	found    bool
	readAt   time.Time
}

// NewCachingRoleMatrix wraps a role matrix in a read-through cache.
func NewCachingRoleMatrix(cfg CacheConfig) *CachingRoleMatrix {
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	metrics := cfg.Metrics
	if metrics == nil {
		metrics = noopCacheMetrics{}
	}
	return &CachingRoleMatrix{
		inner:     cfg.Matrix,
		ttl:       ttl,
		now:       now,
		metrics:   metrics,
		roles:     make(map[roleCacheKey]cachedActions),
		revisions: make(map[string]cachedRevision),
	}
}

// ActiveRolePermissions serves what it can from memory and reads through for the
// rest.
//
// The miss is issued as one query for every uncached role rather than one query
// per role: a cold principal with seventy roles must still cost one round trip.
func (c *CachingRoleMatrix) ActiveRolePermissions(ctx context.Context, query assignmentstore.ActiveRolePermissionQuery) ([]assignmentstore.RolePermission, error) {
	now := c.now()

	var resolved []assignmentstore.RolePermission
	var missing []string

	c.mu.Lock()
	for _, role := range query.RoleExternalIDs {
		key := roleCacheKey{query.TenantID, role, query.ResourceKey}
		entry, cached := c.roles[key]
		if cached && now.Sub(entry.readAt) < c.ttl {
			resolved = append(resolved, entry.permissions...)
			continue
		}
		missing = append(missing, role)
	}
	c.mu.Unlock()

	if len(missing) == 0 {
		c.metrics.Hit()
		return resolved, nil
	}
	c.metrics.Miss()

	miss := query
	miss.RoleExternalIDs = missing
	fresh, err := c.inner.ActiveRolePermissions(ctx, miss)
	if err != nil {
		// Deliberately nothing is stored. Remembering a failure as "this role
		// grants nothing" would turn a transient outage into a lasting denial.
		return nil, err
	}

	// Every role that was asked for gets an entry, including the ones the
	// database had nothing for: "this role grants nothing here" is an answer
	// worth caching, and without it an unprivileged principal would read
	// through on every single decision.
	byRole := make(map[string][]assignmentstore.RolePermission, len(missing))
	for _, role := range missing {
		byRole[role] = nil
	}
	for _, permission := range fresh {
		role := permission.Key.RoleExternalID
		byRole[role] = append(byRole[role], permission)
	}

	c.mu.Lock()
	for role, permissions := range byRole {
		c.roles[roleCacheKey{query.TenantID, role, query.ResourceKey}] = cachedActions{
			permissions: permissions,
			readAt:      now,
		}
	}
	c.mu.Unlock()

	return append(resolved, fresh...), nil
}

// PermissionRevision reads a tenant's revision through the same cache. Every
// decision reports it, so reading it from the database each time would put a
// second round trip on the hot path.
func (c *CachingRoleMatrix) PermissionRevision(ctx context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error) {
	now := c.now()

	c.mu.Lock()
	entry, cached := c.revisions[tenantID]
	c.mu.Unlock()
	if cached && now.Sub(entry.readAt) < c.ttl {
		return entry.revision, entry.found, nil
	}

	revision, found, err := c.inner.PermissionRevision(ctx, tenantID)
	if err != nil {
		return assignmentstore.PermissionRevision{}, false, err
	}

	c.mu.Lock()
	c.revisions[tenantID] = cachedRevision{revision: revision, found: found, readAt: now}
	c.mu.Unlock()

	return revision, found, nil
}

// InvalidateRole drops every cached entry for one role within one tenant,
// across every resource it was cached under (§10.1's "every ADS replica
// invalidates only the affected role or user cache keys"). An entry for a
// different role, or the same role in a different tenant, is left exactly
// as it was.
func (c *CachingRoleMatrix) InvalidateRole(tenantID, roleID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.roles {
		if key.tenantID == tenantID && key.roleID == roleID {
			delete(c.roles, key)
		}
	}
}

// InvalidateRevision drops the cached permission revision for one tenant,
// forcing the next read through to the database.
func (c *CachingRoleMatrix) InvalidateRevision(tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.revisions, tenantID)
}

// InvalidateTenant drops every cached entry for one tenant - every role's
// permissions and the revision. It is the reconciler's tool (§10.3): a
// revision mismatch means something in the tenant changed, but the
// reconciler does not know which role, so it cannot invalidate a narrower
// key than the whole tenant.
func (c *CachingRoleMatrix) InvalidateTenant(tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.roles {
		if key.tenantID == tenantID {
			delete(c.roles, key)
		}
	}
	delete(c.revisions, tenantID)
}

// CachedRevision reports the revision currently cached for a tenant, without
// reading through on a miss - the reconciler needs to know what is cached,
// not to warm what is not.
func (c *CachingRoleMatrix) CachedRevision(tenantID string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, cached := c.revisions[tenantID]
	if !cached || !entry.found {
		return 0, false
	}
	return entry.revision.Revision, true
}

// KnownTenants lists every tenant this cache currently holds any entry for -
// a revision, or at least one role's permissions. The reconciler uses this to
// know which tenants to check without needing its own enumeration of every
// tenant in the system: a tenant this replica has never served has nothing
// to reconcile.
func (c *CachingRoleMatrix) KnownTenants() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := make(map[string]struct{})
	for key := range c.roles {
		seen[key.tenantID] = struct{}{}
	}
	for tenantID := range c.revisions {
		seen[tenantID] = struct{}{}
	}
	tenants := make([]string, 0, len(seen))
	for tenantID := range seen {
		tenants = append(tenants, tenantID)
	}
	return tenants
}
