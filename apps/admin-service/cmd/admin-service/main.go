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

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/adsclient"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/auditsearch"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/catalogapi"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/config"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/platformstatus"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/rolematrix"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/server"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/simulate"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/useroverride"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/postgresstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/console"
	idpprovider "github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/provider"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	electionprovider "github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/provider"
	"github.com/tishan-harischandra/cerbos-poc/libs/outbox"
	"github.com/tishan-harischandra/cerbos-poc/libs/outbox/kafkapublisher"
	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
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

	resourceCatalog, err := capabilitycatalog.LoadResourceCatalogDir(cfg.CatalogDir)
	if err != nil {
		logger.Error("could not load the resource catalog for the browser", slog.Any("error", err))
		os.Exit(1)
	}

	uiCapabilities, err := capabilitycatalog.LoadDefinitionsDir(cfg.CapabilityCatalogDir)
	if err != nil {
		logger.Error("could not load the UI capability catalog for the impact index", slog.Any("error", err))
		os.Exit(1)
	}

	// The identity provider is selected here and nowhere else (§7.1), the
	// same as every other service that verifies a token.
	idpConfig, err := idpprovider.FromEnv(os.LookupEnv)
	if err != nil {
		logger.Error("could not read the identity provider configuration", slog.Any("error", err))
		os.Exit(1)
	}
	verifier, err := idpprovider.NewVerifier(idpConfig)
	if err != nil {
		logger.Error("could not prepare token verification", slog.Any("error", err))
		os.Exit(1)
	}

	// The outbox publisher is a singleton across replicas, so this service
	// needs an elector for the same reason the policy controller does.
	// There is no default type: an unset LEADER_ELECTION_TYPE would mean
	// every replica publishing every row.
	electionConfig, err := electionprovider.FromEnv(os.LookupEnv)
	if err != nil {
		logger.Error("could not read the leader election configuration", slog.Any("error", err))
		os.Exit(1)
	}
	// Without this the one failure that matters is silent: the election is
	// unheld, so nothing drains the outbox, while /readyz keeps answering
	// and every write still commits.
	electionConfig.OnError = func(err error) {
		logger.Error("outbox publisher election attempt failed", slog.Any("error", err))
	}
	elector, err := electionprovider.New(electionConfig)
	if err != nil {
		logger.Error("could not prepare leader election", slog.Any("error", err))
		os.Exit(1)
	}

	// The console is served from this process (ADR-008), so its bundle is
	// optional configuration rather than a separate deployment: a build
	// with nothing on disk still serves the administration API.
	var consoleConfig *console.Config
	if cfg.ConsoleDir != "" {
		consoleConfig = &console.Config{
			Dir:     cfg.ConsoleDir,
			ADSAddr: cfg.ADSAddr,
			Environment: console.Environment{
				OIDCIssuer:   cfg.OIDCIssuer,
				OIDCClientID: cfg.OIDCClientID,
			},
		}
	}

	handler, err := server.New(server.Config{
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
		Catalog: &catalogapi.Handler{
			Resources:          resourceCatalog,
			RootPolicyRevision: cfg.RootPolicyRevision,
			Capabilities:       uiCapabilities,
		},
		Simulate: &simulate.Handler{
			ADS: adsclient.New(cfg.ADSAddr),
		},
		AuditSearch: &auditsearch.Handler{
			Store: store,
		},
		PlatformStatus: &platformstatus.Handler{
			PolicyStore:          policyrelease.NewStore(cfg.PolicyReleaseStoreDir),
			ADS:                  adsclient.New(cfg.ADSAddr),
			IdPType:              string(idpConfig.Type),
			IdPRoleSource:        string(idpConfig.RoleSource),
			IdPTenantMappingMode: string(idpConfig.TenantMappingMode),
		},
		Console: consoleConfig,
	})
	if err != nil {
		logger.Error("could not build the http surface", slog.Any("error", err))
		os.Exit(1)
	}

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
	// Only the leader drains. Every replica reading the same batch is
	// inside the at-least-once contract and the invalidation consumer is
	// idempotent, so this is not a correctness fix - it is the difference
	// between publishing each event once and publishing it once per
	// replica, which the prod overlay scales to eight.
	//
	// The loop itself knows nothing about any of this: it is the work
	// passed to onElected, and it already returns on ctx.Done, so losing
	// the election stops it mid-drain.
	go func() {
		if err := elector.Run(ctx, leaderlock.ElectionOutboxPublisher, outboxLoop.Run); err != nil {
			logger.Error("the outbox publisher election stopped", slog.Any("error", err))
		}
	}()

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
		slog.String("leaderElection", string(electionConfig.Type)),
		slog.String("consoleDir", cfg.ConsoleDir),
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
