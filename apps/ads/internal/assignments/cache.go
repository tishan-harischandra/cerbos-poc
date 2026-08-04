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
type CachingRoleMatrix struct {
	inner RoleMatrix
	ttl   time.Duration

	// Now is the clock entries are aged against. Exported so tests can advance
	// time without sleeping.
	Now func() time.Time

	mu        sync.Mutex
	roles     map[roleCacheKey]cachedActions
	revisions map[string]cachedRevision
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

// NewCachingRoleMatrix wraps a role matrix in a read-through cache. A
// non-positive TTL falls back to DefaultCacheTTL.
func NewCachingRoleMatrix(inner RoleMatrix, ttl time.Duration) *CachingRoleMatrix {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &CachingRoleMatrix{
		inner:     inner,
		ttl:       ttl,
		Now:       time.Now,
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
	now := c.Now()

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
		return resolved, nil
	}

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
	now := c.Now()

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
