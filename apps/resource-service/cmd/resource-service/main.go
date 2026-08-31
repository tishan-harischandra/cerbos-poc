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
	// The tenant (realm, issuer, client) comes from the tenant registry
	// (issue #76) rather than IDP_REALM/IDP_TENANT_ID/IDP_ISSUER.
	tenantCtx, cancelTenantCtx := context.WithTimeout(context.Background(), 90*time.Second)
	tenant, err := tenantresolve.Single(tenantCtx, store)
	cancelTenantCtx()
	if err != nil {
		logger.Error("could not resolve the tenant registry", slog.Any("error", err))
		os.Exit(1)
	}
	idpConfig, err := provider.ConfigFromTenant(os.LookupEnv, provider.Tenant{
		Realm:               tenant.Realm,
		Issuer:              tenant.Issuer,
		BrowserClientID:     tenant.BrowserClientID,
		ServiceClientID:     tenant.ServiceClientID,
		CredentialSecretRef: tenant.CredentialSecretRef,
	})
	if err != nil {
		logger.Error("could not read the identity provider configuration", slog.Any("error", err))
		os.Exit(1)
	}
	verifier, err := provider.NewVerifier(idpConfig)
	if err != nil {
		logger.Error("could not prepare token verification", slog.Any("error", err))
		os.Exit(1)
	}

	authenticated := func(next http.Handler) http.Handler {
		return tokenauth.Require(tokenauth.Config{Verifier: verifier, Logger: logger}, next)
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
