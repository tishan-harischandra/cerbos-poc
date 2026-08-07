// Package cacheconvergence exposes one ADS replica's own cache convergence
// state to the Admin Console's revision and activation module (issue #22):
// the tenant revision this replica currently has cached against the one
// PostgreSQL, the same reconciler.RevisionSource, actually holds.
package cacheconvergence

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// Cache is the slice of CachingRoleMatrix this handler reads.
type Cache interface {
	CachedRevision(tenantID string) (int64, bool)
}

// Store is the authoritative revision source, the same one the reconciler
// compares the cache against.
type Store interface {
	PermissionRevision(ctx context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error)
}

// Config holds the handler's collaborators.
type Config struct {
	Cache Cache
	Store Store
}

type response struct {
	Tenant               string `json:"tenant"`
	CachedRevision       int64  `json:"cachedRevision"`
	ActualRevision       int64  `json:"actualRevision"`
	Converged            bool   `json:"converged"`
	ReplicasBehindTarget int    `json:"replicasBehindTarget"`
}

// NewHandler serves GET /internal/cache/convergence: the caller's own
// tenant's cached revision against the actual one, and whether this replica
// (the only one this compose topology runs) is behind.
func NewHandler(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := tokenauth.From(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "a bearer token is required")
			return
		}

		actual, found, err := cfg.Store.PermissionRevision(r.Context(), identity.TenantID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "the authorization database could not be reached")
			return
		}
		actualRevision := int64(0)
		if found {
			actualRevision = actual.Revision
		}

		cachedRevision, cached := cfg.Cache.CachedRevision(identity.TenantID)
		// A tenant this replica has never served has nothing cached to drift:
		// the next read simply misses and fetches fresh (§10.3), so it is
		// reported converged rather than behind.
		converged := !cached || cachedRevision == actualRevision

		replicasBehind := 0
		if !converged {
			replicasBehind = 1
		}

		writeJSON(w, http.StatusOK, response{
			Tenant:               identity.TenantID,
			CachedRevision:       cachedRevision,
			ActualRevision:       actualRevision,
			Converged:            converged,
			ReplicasBehindTarget: replicasBehind,
		})
	})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
