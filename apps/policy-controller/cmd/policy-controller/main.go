// Command policy-controller is the Policy Sync and Release Controller
// (§13): it polls Gitea for a protected root policy tag, validates and
// packages the referenced commit, and installs and activates it across the
// Cerbos fleet through the pod-local Admin API.
//
// Exactly one instance acts on any tick; every other instance stays passive
// behind a PostgreSQL advisory lock (leader.Elector), because compose has no
// leader election of its own and the mechanism has to be real and portable
// to a future Kubernetes deployment.
//
// In this compose topology there is exactly one Cerbos replica sharing one
// bind-mounted policy directory with this controller, so the leader plays
// both the central-orchestration role and the per-pod install role in one
// process. A true multi-pod deployment would split the per-pod install and
// reload into a sidecar that consumes the archive this controller (as
// leader) produces - that split is Kubernetes work this issue does not
// cover.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/policy-controller/internal/config"
	"github.com/tishan-harischandra/cerbos-poc/apps/policy-controller/internal/leader"
	"github.com/tishan-harischandra/cerbos-poc/apps/policy-controller/internal/server"
	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

// leaderLockKey identifies the advisory lock every policy-controller
// instance contends for. It has no meaning beyond being a stable, unique
// key for this one lock.
const leaderLockKey = 726_314_009

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.FromEnv(os.LookupEnv)
	status := server.NewStatus()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.New(status),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("policy-controller http server stopped", slog.Any("error", err))
		}
	}()

	elector, err := leader.New(ctx, cfg.PostgresDSN, leaderLockKey)
	if err != nil {
		logger.Error("could not prepare leader election", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = elector.Close(context.Background()) }()

	fetcher := policyrelease.NewGiteaClient(policyrelease.GiteaConfig{
		BaseURL: cfg.GiteaBaseURL,
		Repo:    cfg.GiteaRepo,
		Token:   cfg.GiteaToken,
	})
	compiler := policyrelease.NewCLICompiler(cfg.CerbosBinary)
	store := policyrelease.NewStore(cfg.ArchiveStoreDir)
	reloader := policyrelease.CerbosAdminReloader{}

	replicas := make([]policyrelease.Replica, 0, len(cfg.CerbosAdminAddresses))
	for _, addr := range cfg.CerbosAdminAddresses {
		replicas = append(replicas, policyrelease.Replica{
			PolicyDir: cfg.PolicyDir,
			Admin: policyrelease.AdminEndpoint{
				Name:         addr,
				Address:      addr,
				Username:     cfg.CerbosAdminUsername,
				Password:     cfg.CerbosAdminPassword,
				PlaintextTLS: cfg.CerbosAdminPlaintext,
			},
		})
	}

	releaseCfg := policyrelease.ReleaseConfig{
		Fetcher:     fetcher,
		TagPrefix:   cfg.TagPrefix,
		Validate:    policyrelease.ValidateOptions{Compiler: compiler},
		Replicas:    replicas,
		Reloader:    reloader,
		Store:       store,
		RetainCount: cfg.RetainCount,
		WorkDir:     cfg.WorkDir,
	}

	logger.Info("policy-controller starting",
		slog.String("gitea", cfg.GiteaBaseURL),
		slog.String("repo", cfg.GiteaRepo),
		slog.String("tagPrefix", cfg.TagPrefix),
		slog.Duration("pollInterval", cfg.PollInterval),
	)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	// Run the first tick immediately rather than waiting a full interval
	// after startup.
	tick(ctx, logger, status, elector, releaseCfg)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick(ctx, logger, status, elector, releaseCfg)
		}
	}
}

// tick contends for leadership and, if won, runs one pass of the release
// pipeline. A passive instance still reports its (non-)leadership on
// /readyz.
func tick(ctx context.Context, logger *slog.Logger, status *server.Status, elector *leader.Elector, cfg policyrelease.ReleaseConfig) {
	acquired, err := elector.TryAcquire(ctx)
	if err != nil {
		logger.Error("leader election attempt failed", slog.Any("error", err))
		return
	}
	status.SetLeader(acquired)
	if !acquired {
		return
	}

	result, err := policyrelease.RunOnce(ctx, cfg)
	if err != nil {
		logger.Error("release pipeline run failed", slog.Any("error", err), slog.String("revision", result.Revision))
		status.SetLastResult(result.Revision, false, err.Error())
		return
	}

	logger.Info("release pipeline run succeeded",
		slog.String("revision", result.Revision),
		slog.Any("confirmedReplicas", result.Confirmed),
	)
	status.SetLastResult(result.Revision, true, "")
}
