// Package server exposes the resource service's HTTP surface.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
)

// EnvJSPath is where business-ui's index.html expects the runtime
// environment, mirroring apps/admin-service/internal/console's own
// EnvJSPath (ADR-008's pattern, extended to a second front end by issue
// #83's per-tenant resolution).
const EnvJSPath = "/assets/env.js"

// DefaultReadinessTimeout bounds how long readiness waits on its
// dependencies, mirroring apps/ads/internal/server.
const DefaultReadinessTimeout = 3 * time.Second

// Dependency is a downstream the resource service needs before it can serve
// traffic.
type Dependency struct {
	Name  string
	Probe func(context.Context) error
}

// Config holds the collaborators the HTTP surface depends on.
type Config struct {
	Dependencies     []Dependency
	ReadinessTimeout time.Duration
	// FHIRHandler serves every /fhir/... route. It arrives already wrapped
	// in the token middleware, the same convention apps/ads uses: no route
	// exists that forgot authentication and no route needs it applied twice.
	FHIRHandler http.Handler
	// HostResolver resolves business-ui's runtime environment per tenant,
	// from the request's own Host header (issue #83) - nil means this
	// deployment does not serve the console's env.js at all (a bare API
	// deployment, or a business-ui served some other way).
	HostResolver tenantregistry.HostResolver
}

// New builds the resource service's HTTP handler.
func New(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	if cfg.FHIRHandler != nil {
		mux.Handle("/fhir/", cfg.FHIRHandler)
	}
	if cfg.HostResolver != nil {
		mux.Handle("GET "+EnvJSPath, tenantregistry.EnvJSHandler(cfg.HostResolver))
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
