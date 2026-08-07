package cacheconvergence_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/cacheconvergence"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

type fakeCache struct {
	revision int64
	cached   bool
}

func (f *fakeCache) CachedRevision(tenantID string) (int64, bool) {
	return f.revision, f.cached
}

type fakeStore struct {
	revision assignmentstore.PermissionRevision
	found    bool
	err      error
}

func (f *fakeStore) PermissionRevision(ctx context.Context, tenantID string) (assignmentstore.PermissionRevision, bool, error) {
	return f.revision, f.found, f.err
}

func get(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	return request.WithContext(tokenauth.WithIdentity(request.Context(), tokenauth.Identity{
		PrincipalID: "user-admin", TenantID: "tenant-a",
	}))
}

func TestConvergence_ReportsConvergedWhenCachedMatchesActual(t *testing.T) {
	handler := cacheconvergence.NewHandler(cacheconvergence.Config{
		Cache: &fakeCache{revision: 4, cached: true},
		Store: &fakeStore{revision: assignmentstore.PermissionRevision{Revision: 4}, found: true},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get("/internal/cache/convergence"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	var body struct {
		Tenant          string `json:"tenant"`
		CachedRevision  int64  `json:"cachedRevision"`
		ActualRevision  int64  `json:"actualRevision"`
		Converged       bool   `json:"converged"`
		ReplicasBehind  int    `json:"replicasBehindTarget"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.Tenant != "tenant-a" || body.CachedRevision != 4 || body.ActualRevision != 4 || !body.Converged {
		t.Fatalf("body = %+v, want converged tenant-a at revision 4", body)
	}
	if body.ReplicasBehind != 0 {
		t.Fatalf("replicasBehindTarget = %d, want 0", body.ReplicasBehind)
	}
}

func TestConvergence_ReportsNotConvergedWhenCachedLagsActual(t *testing.T) {
	handler := cacheconvergence.NewHandler(cacheconvergence.Config{
		Cache: &fakeCache{revision: 3, cached: true},
		Store: &fakeStore{revision: assignmentstore.PermissionRevision{Revision: 5}, found: true},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get("/internal/cache/convergence"))

	var body struct {
		CachedRevision int64 `json:"cachedRevision"`
		ActualRevision int64 `json:"actualRevision"`
		Converged      bool  `json:"converged"`
		ReplicasBehind int   `json:"replicasBehindTarget"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.Converged {
		t.Fatal("Converged = true, want false when cached lags actual")
	}
	if body.CachedRevision != 3 || body.ActualRevision != 5 {
		t.Fatalf("body = %+v", body)
	}
	if body.ReplicasBehind != 1 {
		t.Fatalf("replicasBehindTarget = %d, want 1", body.ReplicasBehind)
	}
}

func TestConvergence_AnUncachedTenantIsReportedConverged(t *testing.T) {
	// A tenant this replica has never served has nothing cached to drift; the
	// next read simply misses and fetches fresh, so there is no meaningful
	// "behind target" state for it.
	handler := cacheconvergence.NewHandler(cacheconvergence.Config{
		Cache: &fakeCache{cached: false},
		Store: &fakeStore{revision: assignmentstore.PermissionRevision{Revision: 5}, found: true},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get("/internal/cache/convergence"))

	var body struct {
		Converged bool `json:"converged"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if !body.Converged {
		t.Fatal("Converged = false, want true for a never-cached tenant")
	}
}

func TestConvergence_RequiresABearerToken(t *testing.T) {
	handler := cacheconvergence.NewHandler(cacheconvergence.Config{
		Cache: &fakeCache{},
		Store: &fakeStore{},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/cache/convergence", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
