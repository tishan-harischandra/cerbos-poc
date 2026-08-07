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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/assignments"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/authz"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/cacheconvergence"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/capability"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/cerbos"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/config"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/directoryapi"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/invalidation"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/invalidationmetrics"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/netprobe"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/server"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/postgresstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/provider"
)

// invalidationCaches presents the role-permission and user-override caches
// as one invalidation.Cache and one invalidation.ReconcilerCache: the
// outbox consumer and the reconciler each need to act on whichever of the
// two an event or a drift actually names, without either of them knowing
// there are two.
type invalidationCaches struct {
	roles     *assignments.CachingRoleMatrix
	overrides *assignments.CachingOverrides
}

func (c invalidationCaches) InvalidateRole(tenantID, roleID string) {
	c.roles.InvalidateRole(tenantID, roleID)
}

func (c invalidationCaches) InvalidateRevision(tenantID string) {
	c.roles.InvalidateRevision(tenantID)
}

func (c invalidationCaches) InvalidateUser(tenantID, userID string) {
	c.overrides.InvalidateUser(tenantID, userID)
}

func (c invalidationCaches) KnownTenants() []string {
	return c.roles.KnownTenants()
}

func (c invalidationCaches) CachedRevision(tenantID string) (int64, bool) {
	return c.roles.CachedRevision(tenantID)
}

// InvalidateTenant drops every cached entry for one tenant in both caches -
// the reconciler's tool (§10.3) for a drifted revision it cannot narrow to
// one role or one user.
func (c invalidationCaches) InvalidateTenant(tenantID string) {
	c.roles.InvalidateTenant(tenantID)
	c.overrides.InvalidateTenant(tenantID)
}

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

	// One shared cache behind both the authz and capability handlers
	// (§10.1, §11.2): a targeted invalidation or a reconciliation pass has
	// to reach every entry this replica holds, and two independent caches
	// would let one of them silently keep serving a stale answer forever.
	roleMatrixCache := assignments.NewCachingRoleMatrix(assignments.CacheConfig{
		Matrix: store,
		TTL:    cfg.RoleMatrixCacheTTL,
	})
	// A separate cache from roleMatrixCache (§17.1's "cache hit ratios for
	// role permissions and user overrides", reported per cache, not as a
	// single aggregate): the two are resolved from different places and
	// invalidated by different outbox events.
	overridesCache := assignments.NewCachingOverrides(assignments.OverrideCacheConfig{
		Overrides: assignments.NewDBOverrides(store, nil),
		TTL:       cfg.RoleMatrixCacheTTL,
	})
	invalidationCache := invalidationCaches{roles: roleMatrixCache, overrides: overridesCache}

	registry := prometheus.NewRegistry()
	invalidationMetrics := invalidationmetrics.New(registry)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// §10.1's convergence path: the consumer applies a targeted
	// invalidation the moment Kafka delivers one, and the reconciler
	// (§10.3) repairs whatever it misses on its own schedule, independent
	// of whether Kafka is reachable at all.
	kafkaReader := invalidation.NewKafkaReader(cfg.KafkaBrokers, cfg.KafkaTopic, cfg.KafkaConsumerGroup)
	consumer := &invalidation.Consumer{
		Reader: kafkaReader,
		Handler: &invalidation.Handler{
			Cache:   invalidationCache,
			Metrics: invalidationMetrics,
		},
		OnError: func(err error) {
			logger.Error("invalidation consumer error", slog.Any("error", err))
		},
	}
	go consumer.Run(ctx)

	// Consumer lag is an observability signal, not something the
	// invalidation path itself acts on - so it is polled on its own
	// schedule rather than threaded through Handler.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				invalidationMetrics.SetConsumerLag(float64(kafkaReader.Lag()))
			}
		}
	}()

	reconciler := invalidation.NewReconciler(invalidation.ReconcilerConfig{
		Cache:    invalidationCache,
		Store:    store,
		Interval: cfg.ReconcileInterval,
		OnDrift: func(tenantID string, cached, actual int64) {
			logger.Info("reconciler repaired a drifted tenant",
				slog.String("tenantId", tenantID), slog.Int64("cached", cached), slog.Int64("actual", actual))
		},
		OnError: func(err error) {
			logger.Error("reconciler error", slog.Any("error", err))
		},
	})
	go reconciler.Run(ctx)

	handler := server.New(server.Config{
		Dependencies: []server.Dependency{
			{Name: "cerbos", Probe: cerbos.NewGRPCProbe(cfg.CerbosGRPCAddr)},
			{Name: "postgres", Probe: netprobe.NewTCPProbe(cfg.PostgresAddr)},
			{Name: "idp", Probe: netprobe.NewTCPProbe(cfg.IdPAddr)},
		},
		AuthzHandler: authenticated(authz.NewHandler(authz.Config{
			PDP: pdp,
			Assignments: assignments.NewResolver(assignments.ResolverConfig{
				Matrix:    roleMatrixCache,
				Overrides: overridesCache,
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
		DirectoryUserRolesHandler: authenticated(directoryapi.NewUserRolesHandler(directoryapi.Config{
			Directory: directory,
			Logger:    logger,
		})),
		CapabilityHandler: authenticated(capability.NewHandler(capability.Config{
			PDP:               pdp,
			CapabilityCatalog: capability.NewFSCatalog(cfg.CapabilityCatalogDir, cfg.CapabilityCatalogRevision, nil),
			TargetResolver:    capability.DefaultTargetResolver{},
			Assignments: assignments.NewResolver(assignments.ResolverConfig{
				Matrix:    roleMatrixCache,
				Overrides: overridesCache,
			}),
			RootPolicyRevision: cfg.RootPolicyRevision,
			Logger:             logger,
		})),
		SimulateHandler: authenticated(authz.NewSimulateHandler(authz.Config{
			PDP: pdp,
			Assignments: assignments.NewResolver(assignments.ResolverConfig{
				Matrix:    roleMatrixCache,
				Overrides: overridesCache,
			}),
			Logger: logger,
		})),
		CapabilitySimulateHandler: authenticated(capability.NewSimulateHandler(capability.Config{
			PDP:               pdp,
			CapabilityCatalog: capability.NewFSCatalog(cfg.CapabilityCatalogDir, cfg.CapabilityCatalogRevision, nil),
			Assignments: assignments.NewResolver(assignments.ResolverConfig{
				Matrix:    roleMatrixCache,
				Overrides: overridesCache,
			}),
			RootPolicyRevision: cfg.RootPolicyRevision,
			Logger:             logger,
		})),
		MetricsHandler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
		CacheConvergenceHandler: authenticated(cacheconvergence.NewHandler(cacheconvergence.Config{
			Cache: roleMatrixCache,
			Store: store,
		})),
	})

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

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
