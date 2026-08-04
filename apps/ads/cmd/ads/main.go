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
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/cerbos"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/config"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/netprobe"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/server"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/postgresstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
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

	handler := server.New(server.Config{
		Dependencies: []server.Dependency{
			{Name: "cerbos", Probe: cerbos.NewGRPCProbe(cfg.CerbosGRPCAddr)},
			{Name: "postgres", Probe: netprobe.NewTCPProbe(cfg.PostgresAddr)},
		},
		AuthzHandler: authz.NewHandler(authz.Config{
			PDP: pdp,
			Assignments: assignments.NewResolver(assignments.ResolverConfig{
				Matrix:    assignments.NewCachingRoleMatrix(store, cfg.RoleMatrixCacheTTL),
				Overrides: assignments.NewSeededOverrides(),
			}),
			Logger: logger,
		}),
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
		slog.Duration("roleMatrixCacheTtl", cfg.RoleMatrixCacheTTL),
		slog.String("seededPrincipals", assignments.Describe()),
	)

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("ads stopped", slog.Any("error", err))
		os.Exit(1)
	}
}
