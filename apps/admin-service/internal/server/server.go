// Package server exposes the Authorization Administration Service HTTP
// surface (§9.4).
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/catalogapi"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/rolematrix"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/useroverride"
)

// DefaultReadinessTimeout bounds how long readiness waits on its dependencies.
const DefaultReadinessTimeout = 3 * time.Second

// Dependency is a downstream the service needs before it can serve traffic.
type Dependency struct {
	Name  string
	Probe func(context.Context) error
}

// Config holds the collaborators the HTTP surface depends on.
type Config struct {
	Dependencies []Dependency

	// ReadinessTimeout bounds a single /readyz probe round. Zero means
	// DefaultReadinessTimeout.
	ReadinessTimeout time.Duration

	// Verifier authenticates every /admin/authz route. Nil means those
	// routes are not registered at all, so a misconfigured build fails
	// with 404 rather than answering administration questions from an
	// unauthenticated caller.
	Verifier tokenauth.Verifier

	// RoleMatrix serves the role-matrix endpoints. Nil means the routes
	// are not registered.
	RoleMatrix *rolematrix.Handler

	// UserOverride serves the user-override endpoints. Nil means the
	// routes are not registered.
	UserOverride *useroverride.Handler

	// Catalog serves the resource catalog endpoint. Nil means the route
	// is not registered.
	Catalog *catalogapi.Handler
}

// New builds the Administration Service HTTP handler.
func New(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})

	if cfg.Verifier != nil {
		authenticated := func(next http.HandlerFunc) http.Handler {
			return tokenauth.Require(tokenauth.Config{Verifier: cfg.Verifier}, next)
		}
		if cfg.RoleMatrix != nil {
			mux.Handle("PUT /admin/authz/tenants/{tenant}/roles/{role}/permissions",
				authenticated(cfg.RoleMatrix.Save))
			mux.Handle("GET /admin/authz/tenants/{tenant}/roles/{role}/permissions",
				authenticated(cfg.RoleMatrix.Read))
			mux.Handle("GET /admin/authz/tenants/{tenant}/permission-revision",
				authenticated(cfg.RoleMatrix.CurrentRevision))
		}
		if cfg.UserOverride != nil {
			mux.Handle("PUT /admin/authz/tenants/{tenant}/hospitals/{hospital}/users/{user}/overrides",
				authenticated(cfg.UserOverride.Save))
			mux.Handle("GET /admin/authz/tenants/{tenant}/hospitals/{hospital}/users/{user}/overrides",
				authenticated(cfg.UserOverride.Read))
			mux.Handle("POST /admin/authz/tenants/{tenant}/hospitals/{hospital}/users/{user}/overrides/preview",
				authenticated(cfg.UserOverride.Preview))
		}
		if cfg.Catalog != nil {
			mux.Handle("GET /admin/authz/resources", authenticated(cfg.Catalog.Read))
		}
	}

	timeout := cfg.ReadinessTimeout
	if timeout <= 0 {
		timeout = DefaultReadinessTimeout
	}
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		results := make(map[string]string, len(cfg.Dependencies))
		ready := true
		for _, dep := range cfg.Dependencies {
			if err := dep.Probe(ctx); err != nil {
				results[dep.Name] = err.Error()
				ready = false
				continue
			}
			results[dep.Name] = "ok"
		}

		status, code := "ready", http.StatusOK
		if !ready {
			status, code = "unavailable", http.StatusServiceUnavailable
		}
		writeJSON(w, code, map[string]any{"status": status, "dependencies": results})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
