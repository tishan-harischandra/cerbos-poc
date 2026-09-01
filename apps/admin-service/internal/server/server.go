// Package server exposes the Authorization Administration Service HTTP
// surface (§9.4).
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/auditsearch"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/catalogapi"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/console"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/platformstatus"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/rolematrix"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/simulate"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/useroverride"
	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
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

	// Simulate serves the effective-access simulator endpoints (issue
	// #19). Nil means the routes are not registered.
	Simulate *simulate.Handler

	// AuditSearch serves the audit search endpoint (issue #20). Nil means
	// the route is not registered.
	AuditSearch *auditsearch.Handler

	// PlatformStatus serves the revision and activation module plus IdP
	// diagnostics endpoints (issue #22). Nil means the routes are not
	// registered.
	PlatformStatus *platformstatus.Handler

	// Console serves the Admin Console: its bundle, its runtime
	// environment, and the ADS calls it makes through this origin
	// (ADR-008). Nil leaves all of that unregistered, which is what a
	// deployment with no bundle on disk wants - the administration API
	// still answers.
	Console *console.Config
}

// New builds the Administration Service HTTP handler.
//
// It answers on one port for both audiences: the administration API under
// /admin, and the Admin Console it serves to a browser (ADR-008). The console
// calls this same origin under /api, so the browser never reaches the ADS or
// this service's API directly (§16.1).
func New(cfg Config) (http.Handler, error) {
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
			mux.Handle("GET /admin/authz/resources/{resource}/actions/{action}/capabilities",
				authenticated(cfg.Catalog.CapabilityImpact))
		}
		if cfg.Simulate != nil {
			mux.Handle("POST /admin/authz/simulate", authenticated(cfg.Simulate.SimulateAccess))
			mux.Handle("POST /admin/authz/simulate-capabilities", authenticated(cfg.Simulate.SimulateCapabilities))
		}
		if cfg.AuditSearch != nil {
			mux.Handle("GET /admin/authz/audit", authenticated(cfg.AuditSearch.Search))
		}
		if cfg.PlatformStatus != nil {
			mux.Handle("GET /admin/authz/policy-releases", authenticated(cfg.PlatformStatus.PolicyReleases))
			mux.Handle("GET /admin/authz/tenants/{tenant}/convergence", authenticated(cfg.PlatformStatus.Convergence))
			mux.Handle("GET /admin/idp/diagnostics", authenticated(cfg.PlatformStatus.IdPDiagnostics))
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

	if cfg.Console == nil {
		return mux, nil
	}
	return withConsole(mux, *cfg.Console)
}

// withConsole adds the browser-facing surface to the API mux.
//
// The console's routes and the routes they reach are registered on one mux, in
// one file. That is the point of ADR-008: a prefix the console calls and a
// prefix this service registers can no longer disagree in production only,
// because nothing between them is written in another language.
func withConsole(mux *http.ServeMux, cfg console.Config) (http.Handler, error) {
	// Only /api comes off, and the rest goes back through this same mux, so
	// /api/admin/authz/... reaches the /admin/authz/... route it names.
	mux.Handle(console.AdminPrefix, console.StripAPIPrefix(mux))

	proxy, err := console.ADSProxy(cfg.ADSAddr)
	if err != nil {
		return nil, err
	}
	mux.Handle(console.ADSPrefix, proxy)

	// Registered ahead of the bundle: the environment is configuration this
	// service holds, not a file the build produced.
	mux.Handle("GET "+console.EnvJSPath, tenantregistry.EnvJSHandler(cfg.HostResolver))

	assets, err := console.Assets(cfg.Dir)
	if err != nil {
		return nil, err
	}
	mux.Handle("/", assets)

	// The shell answers anything the mux does not recognise, because a
	// deep link is a route only the browser's router knows. That must stop
	// at the API's own namespaces: a mistyped endpoint would otherwise
	// answer 200 with HTML, and a caller would try to parse the login page
	// as JSON. A more specific pattern wins in the mux, so the real routes
	// registered above are unaffected and only the gaps between them reach
	// these.
	mux.Handle("/admin/", http.NotFoundHandler())
	mux.Handle(console.APIPrefix+"/", http.NotFoundHandler())
	return mux, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
