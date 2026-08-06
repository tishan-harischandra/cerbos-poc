// Command admin-service runs the Authorization Administration Service HTTP
// surface (§9.4).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/config"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/rolematrix"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/server"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/useroverride"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/postgresstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/provider"
	"github.com/tishan-harischandra/cerbos-poc/libs/outbox"
	"github.com/tishan-harischandra/cerbos-poc/libs/outbox/kafkapublisher"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.FromEnv(os.LookupEnv)

	store, err := postgresstore.Open(context.Background(), cfg.PostgresDSN)
	if err != nil {
		logger.Error("could not prepare the authorization database pool", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()

	catalog, err := capabilitycatalog.LoadActiveCatalogDir(cfg.CatalogDir)
	if err != nil {
		logger.Error("could not load the active resource/action catalog", slog.Any("error", err))
		os.Exit(1)
	}

	// The identity provider is selected here and nowhere else (§7.1), the
	// same as every other service that verifies a token.
	idpConfig, err := provider.FromEnv(os.LookupEnv)
	if err != nil {
		logger.Error("could not read the identity provider configuration", slog.Any("error", err))
		os.Exit(1)
	}
	verifier, err := provider.NewVerifier(idpConfig)
	if err != nil {
		logger.Error("could not prepare token verification", slog.Any("error", err))
		os.Exit(1)
	}

	handler := server.New(server.Config{
		Dependencies: []server.Dependency{
			{Name: "postgres", Probe: tcpProbe(cfg.PostgresAddr)},
			{Name: "idp", Probe: tcpProbe(cfg.IdPAddr)},
		},
		Verifier: verifier,
		RoleMatrix: &rolematrix.Handler{
			Store:   store,
			Catalog: catalog,
		},
		UserOverride: &useroverride.Handler{
			Store:                   store,
			Catalog:                 catalog,
			HighRiskActions:         cfg.HighRiskActions,
			DefaultHighRiskValidity: cfg.HighRiskOverrideValidity,
		},
	})

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// §10.1's step 5: the outbox publisher sends every PermissionChanged
	// batch to Kafka. A publish failure leaves the row unpublished for the
	// next poll to retry - nothing about SaveRoleMatrix's own commit
	// depends on Kafka being reachable at all.
	outboxLoop := outbox.NewLoop(outbox.LoopConfig{
		Store:     store,
		Publisher: kafkapublisher.New(cfg.KafkaBrokers, cfg.KafkaTopic),
		Interval:  cfg.OutboxPublishInterval,
		OnError: func(err error) {
			logger.Error("outbox publisher error", slog.Any("error", err))
		},
	})
	go outboxLoop.Run(ctx)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", slog.Any("error", err))
		}
	}()

	logger.Info("admin-service listening",
		slog.String("addr", cfg.HTTPAddr),
		slog.String("postgres", cfg.PostgresAddr),
		slog.String("idpType", string(idpConfig.Type)),
		slog.String("idpIssuer", idpConfig.Issuer),
	)

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("admin-service stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

func tcpProbe(addr string) func(context.Context) error {
	return func(ctx context.Context) error {
		dialer := &net.Dialer{}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return conn.Close()
	}
}
