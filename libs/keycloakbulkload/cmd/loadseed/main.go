// Command loadseed writes the §15 load population - demo-sized or the full
// 600,000 users / 42,000,000 role mappings - into a running Keycloak and the
// authorization database.
//
// It is the same generator (libs/loadmodel) at two different Config values;
// LOADSEED_PROFILE selects which one, nothing else about this command
// changes between them.
package main

import (
	"context"
	"crypto/sha1" //nolint:gosec // deterministic identifier derivation, not a security use
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/postgresstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/keycloakbulkload"
	"github.com/tishan-harischandra/cerbos-poc/libs/loadmodel"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("loadseed: %v", err)
	}
}

func run() error {
	profile := envOr("LOADSEED_PROFILE", "demo")
	var cfg loadmodel.Config
	switch profile {
	case "demo":
		cfg = loadmodel.DemoConfig()
	case "load":
		cfg = loadmodel.FullLoadConfig()
	default:
		return fmt.Errorf("LOADSEED_PROFILE must be 'demo' or 'load', got %q", profile)
	}

	pop, err := loadmodel.New(cfg)
	if err != nil {
		return fmt.Errorf("building the population: %w", err)
	}

	dataDir := envOr("LOADSEED_DATA_DIR", "/workspace")
	estimate := keycloakbulkload.PreflightEstimate{
		Users:        cfg.Users,
		RoleMappings: cfg.Users * cfg.RolesPerUser,
	}
	if err := keycloakbulkload.Preflight(estimate, dataDir); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	defer cancel()

	// LOADSEED_SKIP_KEYCLOAK lets a retry re-run only the authorization
	// database seeding step after Keycloak was already bulk-loaded
	// successfully (SaveRolePermission/SaveResource/SaveUserOverride are all
	// upserts on their §8.2 keys, so re-seeding is safe).
	if envOr("LOADSEED_SKIP_KEYCLOAK", "") != "" {
		now := time.Now().UTC()
		if dsn := os.Getenv("ASSIGNMENTSTORE_POSTGRES_DSN"); dsn != "" {
			if err := seedAssignmentStore(ctx, dsn, pop, now); err != nil {
				return fmt.Errorf("seeding the authorization database: %w", err)
			}
		} else {
			return fmt.Errorf("LOADSEED_SKIP_KEYCLOAK is set but ASSIGNMENTSTORE_POSTGRES_DSN is not")
		}
		return nil
	}

	adminURL := os.Getenv("KEYCLOAK_LOADTEST_ADMIN_URL")
	if adminURL == "" {
		return fmt.Errorf("KEYCLOAK_LOADTEST_ADMIN_URL is not set")
	}
	dbDSN := os.Getenv("KEYCLOAK_LOADTEST_DB_DSN")
	if dbDSN == "" {
		return fmt.Errorf("KEYCLOAK_LOADTEST_DB_DSN is not set")
	}
	realm := envOr("KEYCLOAK_LOADTEST_REALM", "tenant-a-loadtest")
	clientID := envOr("KEYCLOAK_LOADTEST_CLIENT_ID", "patient-app")
	password := envOr("LOADSEED_PASSWORD", "Load-Test-Only-P@ss1")

	admin, err := keycloakbulkload.NewAdminClient(keycloakbulkload.AdminConfig{
		BaseURL:       adminURL,
		AdminUser:     envOr("KEYCLOAK_ADMIN", "admin"),
		AdminPassword: envOr("KEYCLOAK_ADMIN_PASSWORD", "change-me"),
	})
	if err != nil {
		return err
	}

	log.Printf("loadseed: ensuring realm %q, client %q and %d roles exist", realm, clientID, len(pop.RoleNames()))
	_, roleIDByName, err := admin.EnsureRealm(ctx, keycloakbulkload.RealmSetup{
		Realm:          realm,
		ClientID:       clientID,
		RoleNames:      pop.RoleNames(),
		PasswordPolicy: keycloakbulkload.LoadTestPasswordPolicy,
	})
	if err != nil {
		return fmt.Errorf("setting up the realm: %w", err)
	}
	realmID, err := admin.RealmID(ctx, realm)
	if err != nil {
		return err
	}

	pool, err := pgxpool.New(ctx, dbDSN)
	if err != nil {
		return fmt.Errorf("connecting to keycloak's database: %w", err)
	}
	defer pool.Close()

	cred, err := keycloakbulkload.NewSharedCredential(password)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	users := make(chan keycloakbulkload.UserRecord, 4*keycloakbulkload.DefaultBatchSize)
	generateErr := make(chan error, 1)
	go func() {
		defer close(users)
		defer close(generateErr)
		pop.Users(func(u loadmodel.User) bool {
			roleIDs := make([]string, len(u.RoleNames))
			for i, name := range u.RoleNames {
				id, ok := roleIDByName[name]
				if !ok {
					generateErr <- fmt.Errorf("role %q was not created in the realm", name)
					return false
				}
				roleIDs[i] = id
			}
			users <- keycloakbulkload.UserRecord{
				ID:         deterministicUserID(realm, u.Username),
				Username:   u.Username,
				FirstName:  u.FirstName,
				LastName:   u.LastName,
				Email:      u.Email,
				TenantID:   u.TenantID,
				HospitalID: u.HospitalID(),
				RoleIDs:    roleIDs,
			}
			return true
		})
	}()

	log.Printf("loadseed: writing %d users / %d role mappings to keycloak (%s profile)",
		cfg.Users, cfg.Users*cfg.RolesPerUser, profile)
	started := time.Now()
	stats, err := keycloakbulkload.BulkLoad(ctx, pool, keycloakbulkload.LoadConfig{
		RealmID:    realmID,
		Credential: cred,
		Now:        now,
	}, users)
	if err != nil {
		return fmt.Errorf("bulk loading keycloak: %w", err)
	}
	if genErr := <-generateErr; genErr != nil {
		return genErr
	}
	elapsed := time.Since(started)
	log.Printf("loadseed: keycloak done - %d users, %d role mappings, %d batches, %s (%.0f users/sec, %.0f mappings/sec)",
		stats.Users, stats.RoleMappings, stats.Batches, elapsed,
		float64(stats.Users)/elapsed.Seconds(), float64(stats.RoleMappings)/elapsed.Seconds())

	if dsn := os.Getenv("ASSIGNMENTSTORE_POSTGRES_DSN"); dsn != "" {
		if err := seedAssignmentStore(ctx, dsn, pop, now); err != nil {
			return fmt.Errorf("seeding the authorization database: %w", err)
		}
	} else {
		log.Print("loadseed: ASSIGNMENTSTORE_POSTGRES_DSN is not set - skipping role_permission/override/resource rows")
	}

	if err := reportFootprint(ctx, pool, dbDSN); err != nil {
		log.Printf("loadseed: could not measure disk footprint: %v", err)
	}

	return nil
}

func seedAssignmentStore(ctx context.Context, dsn string, pop *loadmodel.Population, now time.Time) error {
	store, err := postgresstore.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	if err := store.Ping(ctx); err != nil {
		return fmt.Errorf("the authorization database is not reachable: %w", err)
	}

	started := time.Now()
	for _, permission := range pop.RolePermissions(now) {
		if err := store.SaveRolePermission(ctx, permission); err != nil {
			return fmt.Errorf("saving role_permission %+v: %w", permission.Key, err)
		}
	}
	for _, revision := range pop.PermissionRevisions(now) {
		if err := store.SavePermissionRevision(ctx, revision); err != nil {
			return fmt.Errorf("saving permission_revision for %s: %w", revision.TenantID, err)
		}
	}
	for _, resource := range pop.Resources(now) {
		if err := store.SaveResource(ctx, resource); err != nil {
			return fmt.Errorf("saving resource %s/%s: %w", resource.ResourceType, resource.ResourceID, err)
		}
	}

	cfg := pop.Config()
	overrideRows := 0
	for i := 0; i < cfg.Users; i++ {
		for _, override := range pop.Overrides(i, now) {
			if err := store.SaveUserOverride(ctx, override); err != nil {
				return fmt.Errorf("saving an override for %s: %w", override.Key.UserExternalID, err)
			}
			overrideRows++
		}
	}
	log.Printf("loadseed: authorization database done - %d overrides, %s",
		overrideRows, time.Since(started))
	return nil
}

func reportFootprint(ctx context.Context, pool *pgxpool.Pool, dbDSN string) error {
	var sizeBytes int64
	row := pool.QueryRow(ctx, "SELECT pg_database_size(current_database())")
	if err := row.Scan(&sizeBytes); err != nil {
		return err
	}
	log.Printf("loadseed: keycloak database size is now %.2f GiB", float64(sizeBytes)/(1<<30))
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// deterministicUserID derives a UUID-shaped, deterministic id for a user
// from the realm and username, so the same population generated twice
// writes byte-identical user_entity.id values rather than a fresh random id
// each run.
func deterministicUserID(realm, username string) string {
	h := sha1.New() //nolint:gosec // deterministic identifier derivation, not a security use
	h.Write([]byte(realm))
	h.Write([]byte{0})
	h.Write([]byte(username))
	sum := h.Sum(nil)
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
