// Package server exposes the Assignment Data Service (ADS) HTTP surface.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// DefaultReadinessTimeout bounds how long readiness waits on its dependencies.
// Without a bound, one hung downstream hangs /readyz, which in turn stalls the
// health gate every other service starts behind.
const DefaultReadinessTimeout = 3 * time.Second

// Dependency is a downstream the ADS needs before it can serve traffic.
type Dependency struct {
	Name  string
	Probe func(context.Context) error
}

// Config holds the collaborators the ADS HTTP surface depends on.
type Config struct {
	Dependencies []Dependency

	// ReadinessTimeout bounds a single /readyz probe round. Zero means
	// DefaultReadinessTimeout.
	ReadinessTimeout time.Duration

	// AuthzHandler serves POST /internal/authz/check. When nil the route is
	// not registered at all, so a misconfigured build fails with 404 rather
	// than answering authorization questions from a stub.
	//
	// Every handler below arrives already wrapped in the token middleware.
	// Authentication is not applied here: a route that forgot it would then
	// look identical to one that did not need it.
	AuthzHandler http.Handler

	// DirectoryUsersHandler and DirectoryRolesHandler serve the Admin
	// Console's identity directory reads. Nil when no identity provider is
	// configured, in which case the routes do not exist.
	DirectoryUsersHandler http.Handler
	DirectoryRolesHandler http.Handler
	// DirectoryUserRolesHandler serves the roles directly assigned to one
	// user (issue #17's user-override screen). Nil means the route does
	// not exist.
	DirectoryUserRolesHandler http.Handler

	// CapabilityHandler serves POST /internal/capabilities/evaluate, the
	// §12.3 capability snapshot endpoint (issue #11). Nil means the route
	// does not exist.
	CapabilityHandler http.Handler

	// SimulateHandler serves POST /internal/authz/simulate and
	// CapabilitySimulateHandler serves POST /internal/capabilities/simulate
	// (issue #19). Both are reachable only from other backend services over
	// the internal compose network, never from a browser - nil means the
	// route does not exist.
	SimulateHandler           http.Handler
	CapabilitySimulateHandler http.Handler

	// MetricsHandler serves GET /metrics, the §10 convergence metrics
	// (issue #14). Nil means the route does not exist. Unauthenticated,
	// like every other Prometheus scrape target on this network.
	MetricsHandler http.Handler
}

// New builds the ADS HTTP handler.
func New(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	if cfg.AuthzHandler != nil {
		mux.Handle("POST /internal/authz/check", cfg.AuthzHandler)
	}
	if cfg.DirectoryUsersHandler != nil {
		mux.Handle("GET /internal/directory/users", cfg.DirectoryUsersHandler)
	}
	if cfg.DirectoryRolesHandler != nil {
		mux.Handle("GET /internal/directory/roles", cfg.DirectoryRolesHandler)
	}
	if cfg.DirectoryUserRolesHandler != nil {
		mux.Handle("GET /internal/directory/users/{externalId}/roles", cfg.DirectoryUserRolesHandler)
	}
	if cfg.CapabilityHandler != nil {
		mux.Handle("POST /internal/capabilities/evaluate", cfg.CapabilityHandler)
	}
	if cfg.SimulateHandler != nil {
		mux.Handle("POST /internal/authz/simulate", cfg.SimulateHandler)
	}
	if cfg.CapabilitySimulateHandler != nil {
		mux.Handle("POST /internal/capabilities/simulate", cfg.CapabilitySimulateHandler)
	}
	if cfg.MetricsHandler != nil {
		mux.Handle("GET /metrics", cfg.MetricsHandler)
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
