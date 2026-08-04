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

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/cerbos"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/config"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/netprobe"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.FromEnv(os.LookupEnv)

	handler := server.New(server.Config{
		Dependencies: []server.Dependency{
			{Name: "cerbos", Probe: cerbos.NewGRPCProbe(cfg.CerbosGRPCAddr)},
			{Name: "postgres", Probe: netprobe.NewTCPProbe(cfg.PostgresAddr)},
		},
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
	)

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("ads stopped", slog.Any("error", err))
		os.Exit(1)
	}
}
