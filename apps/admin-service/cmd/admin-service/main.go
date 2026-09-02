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
	"sort"
	"syscall"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/adsclient"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/auditsearch"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/catalogapi"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/config"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/console"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/platformstatus"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/rolematrix"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/server"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/simulate"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/tenantonboarding"
	"github.com/tishan-harischandra/cerbos-poc/apps/admin-service/internal/useroverride"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/postgresstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/tenantresolve"
	"github.com/tishan-harischandra/cerbos-poc/libs/capabilitycatalog"
	idpprovider "github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/provider"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	electionprovider "github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/provider"
	"github.com/tishan-harischandra/cerbos-poc/libs/outbox"
	"github.com/tishan-harischandra/cerbos-poc/libs/outbox/kafkapublisher"
	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
	"github.com/tishan-harischandra/cerbos-poc/libs/tenantregistry"
	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
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
	// same as every other service that verifies a token. A deployment
	// serves every realm the tenant registry names (issue #77): each realm
	// gets its own token verifier, and a token's own issuer - never a
	// claim, never request input - selects which realm's verifier checks
	// it.
	tenantCtx, cancelTenantCtx := context.WithTimeout(context.Background(), 90*time.Second)
	tenants, err := tenantresolve.All(tenantCtx, store)
	cancelTenantCtx()
	if err != nil {
		logger.Error("could not resolve the tenant registry", slog.Any("error", err))
		os.Exit(1)
	}
	providerTenants := make([]idpprovider.Tenant, 0, len(tenants))
	for _, tenant := range tenants {
		providerTenants = append(providerTenants, idpprovider.Tenant{
			Realm:               tenant.Realm,
			Issuer:              tenant.Issuer,
			BrowserClientID:     tenant.BrowserClientID,
			ServiceClientID:     tenant.ServiceClientID,
			CredentialSecretRef: tenant.CredentialSecretRef,
		})
	}
	installations, err := idpprovider.InstallationsFromTenants(os.LookupEnv, providerTenants)
	if err != nil {
		logger.Error("could not read the identity provider configuration", slog.Any("error", err))
		os.Exit(1)
	}

	// homeRealm is used for platform-status reporting only (§9.4): which
	// identity provider type and role source this deployment runs, an
	// installation-wide fact rather than a per-tenant one. It is not the
	// console's login configuration - issue #83 replaced "one baked-in
	// realm" with per-tenant resolution from the request's own host, so
	// there is no longer a single realm the console's own login page is
	// "for".
	homeRealm := homeInstallation(providerTenants, installations)

	// A user reaches their hospital group by its own subdomain and never
	// needs to know what a realm is (issue #83): the console's runtime
	// environment names whichever tenant the browser's own Host header
	// resolves to, from the same registry every other multi-tenant
	// wiring in this service already reads - never a value baked into the
	// bundle or this service's own environment at build/start time.
	registryEntries := make([]tenantregistry.Entry, 0, len(providerTenants))
	for _, tenant := range providerTenants {
		registryEntries = append(registryEntries, tenantregistry.Entry{
			Realm:           tenant.Realm,
			Issuer:          tenant.Issuer,
			BrowserClientID: tenant.BrowserClientID,
		})
	}
	hostResolver := tenantregistry.NewHostResolver(registryEntries)

	verifiers := tokenverifier.NewRegistry()
	for _, installation := range installations {
		verifiers.Register(installation.Config.Issuer, installation.Verifier)
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
			Dir:          cfg.ConsoleDir,
			ADSAddr:      cfg.ADSAddr,
			HostResolver: hostResolver,
		}
	}

	handler, err := server.New(server.Config{
		Dependencies: []server.Dependency{
			{Name: "postgres", Probe: tcpProbe(cfg.PostgresAddr)},
			{Name: "idp", Probe: tcpProbe(cfg.IdPAddr)},
		},
		Verifier: verifiers,
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
			PolicyStore:   policyrelease.NewStore(cfg.PolicyReleaseStoreDir),
			ADS:           adsclient.New(cfg.ADSAddr),
			IdPType:       string(homeRealm.Config.Type),
			IdPRoleSource: string(homeRealm.Config.RoleSource),
		},
		TenantOnboarding: &tenantonboarding.Handler{
			Store: store,
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

	realms := make([]string, 0, len(installations))
	for realm := range installations {
		realms = append(realms, realm)
	}
	logger.Info("admin-service listening",
		slog.String("addr", cfg.HTTPAddr),
		slog.String("postgres", cfg.PostgresAddr),
		slog.String("idpType", string(homeRealm.Config.Type)),
		slog.Any("realms", realms),
		slog.String("leaderElection", string(electionConfig.Type)),
		slog.String("consoleDir", cfg.ConsoleDir),
	)

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("admin-service stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

// homeInstallation picks the console's own login realm: the
// lowest-named realm, so the choice is a repeatable fact about the
// registry rather than Go's unspecified map iteration order. An
// installation that wants a particular realm's login page overrides it
// with OIDC_ISSUER/OIDC_CLIENT_ID rather than relying on this default.
func homeInstallation(tenants []idpprovider.Tenant, installations map[string]idpprovider.Installation) idpprovider.Installation {
	realms := make([]string, 0, len(tenants))
	for _, tenant := range tenants {
		realms = append(realms, tenant.Realm)
	}
	sort.Strings(realms)
	return installations[realms[0]]
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
