// Command policy-controller is the Policy Sync and Release Controller
// (§13): it polls Gitea for a protected root policy tag, validates and
// packages the referenced commit, and installs and activates it across the
// Cerbos fleet through the pod-local Admin API.
//
// Exactly one instance acts on any tick; every other instance stays passive
// behind the leaderlock port. Which mechanism decides that is an operational
// choice this process knows nothing about: it asks the factory for an elector
// and runs its poll loop inside the leadership it is given (ADR-009).
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
	"github.com/tishan-harischandra/cerbos-poc/apps/policy-controller/internal/server"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/provider"
	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

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

	// The coordination mechanism is selected here and nowhere else. There
	// is no default: an unset LEADER_ELECTION_TYPE would mean every
	// replica running the release pipeline at once.
	electionConfig, err := provider.FromEnv(os.LookupEnv)
	if err != nil {
		logger.Error("could not read the leader election configuration", slog.Any("error", err))
		os.Exit(1)
	}
	elector, err := provider.New(electionConfig)
	if err != nil {
		logger.Error("could not prepare leader election", slog.Any("error", err))
		os.Exit(1)
	}

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
		slog.String("leaderElection", string(electionConfig.Type)),
	)

	// A passive instance reports its non-leadership on /readyz from the
	// start, and only ever polls inside the leadership it is handed.
	if err := elector.Run(ctx, leaderlock.ElectionPolicyController, func(leaderCtx context.Context) {
		logger.Info("policy-controller elected", slog.String("identity", electionConfig.Identity))
		status.SetLeader(true)
		defer status.SetLeader(false)
		poll(leaderCtx, logger, status, releaseCfg, cfg.PollInterval)
	}); err != nil {
		logger.Error("leader election stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

// poll runs the release pipeline until leadership ends. Every pass is bounded
// by leaderCtx, so a controller that loses the election stops mid-poll rather
// than finishing a release it is no longer entitled to run.
func poll(leaderCtx context.Context, logger *slog.Logger, status *server.Status, cfg policyrelease.ReleaseConfig, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run the first pass immediately rather than waiting a full interval
	// after being elected.
	runOnce(leaderCtx, logger, status, cfg)
	for {
		select {
		case <-leaderCtx.Done():
			return
		case <-ticker.C:
			runOnce(leaderCtx, logger, status, cfg)
		}
	}
}

func runOnce(ctx context.Context, logger *slog.Logger, status *server.Status, cfg policyrelease.ReleaseConfig) {
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
