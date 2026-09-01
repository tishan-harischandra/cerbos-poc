// Command resource-service runs the generic FHIR resource service that acts
// as the policy enforcement point for every resource type in the catalog
// (issue #9).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/adsclient"
	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/config"
	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/netprobe"
	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/pep"
	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/server"
	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/postgresstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/tenantresolve"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/provider"
	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.FromEnv(os.LookupEnv)

	// The pool is opened once and read/written on every request (mirrors
	// apps/ads's postgresstore.Open reasoning): opening it lazily would put
	// a connection handshake in front of every PEP decision.
	store, err := postgresstore.Open(context.Background(), cfg.PostgresDSN)
	if err != nil {
		logger.Error("could not prepare the authorization database pool", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()

	// The identity provider selection is shared with the ADS (§7.1): the
	// resource service verifies the caller's token itself so it can derive a
	// trustworthy tenant/hospital for Create, in addition to forwarding the
	// same token to the ADS's own decision endpoint.
	//
	// A deployment serves every realm the tenant registry names (issue
	// #77): each realm gets its own token verifier, and a token's own
	// issuer - never a claim, never request input - selects which realm's
	// verifier checks it.
	tenantCtx, cancelTenantCtx := context.WithTimeout(context.Background(), 90*time.Second)
	tenants, err := tenantresolve.All(tenantCtx, store)
	cancelTenantCtx()
	if err != nil {
		logger.Error("could not resolve the tenant registry", slog.Any("error", err))
		os.Exit(1)
	}
	providerTenants := make([]provider.Tenant, 0, len(tenants))
	for _, tenant := range tenants {
		providerTenants = append(providerTenants, provider.Tenant{
			Realm:               tenant.Realm,
			Issuer:              tenant.Issuer,
			BrowserClientID:     tenant.BrowserClientID,
			ServiceClientID:     tenant.ServiceClientID,
			CredentialSecretRef: tenant.CredentialSecretRef,
		})
	}
	installations, err := provider.InstallationsFromTenants(os.LookupEnv, providerTenants)
	if err != nil {
		logger.Error("could not read the identity provider configuration", slog.Any("error", err))
		os.Exit(1)
	}
	verifiers := tokenverifier.NewRegistry()
	for _, installation := range installations {
		verifiers.Register(installation.Config.Issuer, installation.Verifier)
	}

	// business-ui's runtime environment resolves per tenant from the
	// request's own Host header (issue #83), the same registry every
	// other multi-tenant wiring in this service already reads - never a
	// value baked into the bundle at build time.
	registryEntries := make([]tenantregistry.Entry, 0, len(providerTenants))
	for _, tenant := range providerTenants {
		registryEntries = append(registryEntries, tenantregistry.Entry{
			Realm:           tenant.Realm,
			Issuer:          tenant.Issuer,
			BrowserClientID: tenant.BrowserClientID,
		})
	}
	hostResolver := tenantregistry.NewHostResolver(registryEntries)

	authenticated := func(next http.Handler) http.Handler {
		return tokenauth.Require(tokenauth.Config{Verifier: verifiers, Logger: logger}, next)
	}

	handler := server.New(server.Config{
		Dependencies: []server.Dependency{
			{Name: "postgres", Probe: netprobe.NewTCPProbe(cfg.PostgresAddr)},
			{Name: "ads", Probe: netprobe.NewHTTPProbe(cfg.ADSAddr + "/healthz")},
		},
		FHIRHandler: authenticated(pep.NewHandler(pep.Config{
			Store:  store,
			ADS:    adsclient.New(cfg.ADSAddr),
			Logger: logger,
		})),
		HostResolver: hostResolver,
	})

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", slog.Any("error", err))
		}
	}()

	logger.Info("resource-service listening",
		slog.String("addr", cfg.HTTPAddr),
		slog.String("ads", cfg.ADSAddr),
		slog.String("postgres", cfg.PostgresAddr),
	)

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("resource-service stopped", slog.Any("error", err))
		os.Exit(1)
	}
}
