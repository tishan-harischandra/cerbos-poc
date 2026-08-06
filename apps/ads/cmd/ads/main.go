// Command ads runs the Assignment Data Service HTTP surface.
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

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/assignments"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/capability"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/cerbos"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/config"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/directoryapi"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/netprobe"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/server"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/postgresstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/provider"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.FromEnv(os.LookupEnv)

	// One channel to the PDP for the life of the process (ADR-003 §6.4): the
	// decision path is hot, and dialling per request would put a TCP handshake
	// in front of every authorization question.
	pdp, err := cerbosclient.New(cerbosclient.Config{
		Address:      cfg.CerbosGRPCAddr,
		PlaintextTLS: true,
	})
	if err != nil {
		logger.Error("could not prepare the Cerbos channel", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = pdp.Close() }()

	// The pool is opened once and read through on every cache miss (§11.2).
	// Opening it lazily per decision would put a connection handshake in front
	// of the hot path.
	store, err := postgresstore.Open(context.Background(), cfg.PostgresDSN)
	if err != nil {
		logger.Error("could not prepare the authorization database pool", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()

	// The identity provider is selected here and nowhere else (§7.1). Every
	// handler below sees only the port, so switching provider is a change to
	// IDP_TYPE rather than to any of this.
	idpConfig, err := provider.FromEnv(os.LookupEnv)
	if err != nil {
		logger.Error("could not read the identity provider configuration", slog.Any("error", err))
		os.Exit(1)
	}
	directory, err := provider.New(idpConfig)
	if err != nil {
		logger.Error("could not prepare the identity directory", slog.Any("error", err))
		os.Exit(1)
	}
	verifier, err := provider.NewVerifier(idpConfig)
	if err != nil {
		logger.Error("could not prepare token verification", slog.Any("error", err))
		os.Exit(1)
	}

	// One middleware, applied to every route that acts on a caller's behalf.
	// Nothing downstream reads an identity from a request body.
	authenticated := func(next http.Handler) http.Handler {
		return tokenauth.Require(tokenauth.Config{Verifier: verifier, Logger: logger}, next)
	}

	handler := server.New(server.Config{
		Dependencies: []server.Dependency{
			{Name: "cerbos", Probe: cerbos.NewGRPCProbe(cfg.CerbosGRPCAddr)},
			{Name: "postgres", Probe: netprobe.NewTCPProbe(cfg.PostgresAddr)},
			{Name: "idp", Probe: netprobe.NewTCPProbe(cfg.IdPAddr)},
		},
		AuthzHandler: authenticated(authz.NewHandler(authz.Config{
			PDP: pdp,
			Assignments: assignments.NewResolver(assignments.ResolverConfig{
				Matrix: assignments.NewCachingRoleMatrix(assignments.CacheConfig{
					Matrix: store,
					TTL:    cfg.RoleMatrixCacheTTL,
				}),
				Overrides: assignments.NewDBOverrides(store, nil),
			}),
			Logger: logger,
		})),
		DirectoryUsersHandler: authenticated(directoryapi.NewUsersHandler(directoryapi.Config{
			Directory: directory,
			Logger:    logger,
		})),
		DirectoryRolesHandler: authenticated(directoryapi.NewRolesHandler(directoryapi.Config{
			Directory: directory,
			Logger:    logger,
		})),
		CapabilityHandler: authenticated(capability.NewHandler(capability.Config{
			PDP:               pdp,
			CapabilityCatalog: capability.NewFSCatalog(cfg.CapabilityCatalogDir, cfg.CapabilityCatalogRevision, nil),
			TargetResolver:    capability.DefaultTargetResolver{},
			Assignments: assignments.NewResolver(assignments.ResolverConfig{
				Matrix: assignments.NewCachingRoleMatrix(assignments.CacheConfig{
					Matrix: store,
					TTL:    cfg.RoleMatrixCacheTTL,
				}),
				Overrides: assignments.NewDBOverrides(store, nil),
			}),
			RootPolicyRevision: cfg.RootPolicyRevision,
			Logger:             logger,
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

	logger.Info("ads listening",
		slog.String("addr", cfg.HTTPAddr),
		slog.String("cerbos", cfg.CerbosGRPCAddr),
		slog.String("postgres", cfg.PostgresAddr),
		slog.String("idpType", string(idpConfig.Type)),
		slog.String("idpIssuer", idpConfig.Issuer),
		slog.Duration("roleMatrixCacheTtl", cfg.RoleMatrixCacheTTL),
	)

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("ads stopped", slog.Any("error", err))
		os.Exit(1)
	}
}
