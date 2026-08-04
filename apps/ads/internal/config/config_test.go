package config_test

import (
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/config"
)

// The DSN carries a password, so it comes from the environment whole rather than
// being assembled here from parts. There is no useful default: a service that
// silently fell back to some built-in connection string would either fail late
// or, worse, reach a database nobody meant it to.
func TestTheAuthorizationDatabaseDSNComesFromTheEnvironment(t *testing.T) {
	cfg := config.FromEnv(func(key string) (string, bool) {
		if key == "ASSIGNMENTSTORE_POSTGRES_DSN" {
			return "postgres://user:secret@db.internal:5432/authz", true
		}
		return "", false
	})

	if cfg.PostgresDSN != "postgres://user:secret@db.internal:5432/authz" {
		t.Errorf("PostgresDSN = %q, want the configured DSN", cfg.PostgresDSN)
	}
}

func TestTheRoleMatrixCacheLifetimeIsConfigurable(t *testing.T) {
	cfg := config.FromEnv(func(key string) (string, bool) {
		if key == "ADS_ROLE_MATRIX_CACHE_TTL" {
			return "5s", true
		}
		return "", false
	})

	if cfg.RoleMatrixCacheTTL != 5*time.Second {
		t.Errorf("RoleMatrixCacheTTL = %s, want 5s", cfg.RoleMatrixCacheTTL)
	}
}

// An unparseable duration must not silently become zero, which would disable the
// cache and put the database on the hot path of every decision.
func TestAnUnreadableCacheLifetimeFallsBackToTheDefault(t *testing.T) {
	cfg := config.FromEnv(func(key string) (string, bool) {
		if key == "ADS_ROLE_MATRIX_CACHE_TTL" {
			return "not-a-duration", true
		}
		return "", false
	})

	if cfg.RoleMatrixCacheTTL != config.DefaultRoleMatrixCacheTTL {
		t.Errorf("RoleMatrixCacheTTL = %s, want the default %s",
			cfg.RoleMatrixCacheTTL, config.DefaultRoleMatrixCacheTTL)
	}
}

func TestFromEnvFallsBackToTheComposeDefaults(t *testing.T) {
	cfg := config.FromEnv(func(string) (string, bool) { return "", false })

	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.CerbosGRPCAddr != "cerbos:3593" {
		t.Errorf("CerbosGRPCAddr = %q, want %q", cfg.CerbosGRPCAddr, "cerbos:3593")
	}
	if cfg.PostgresAddr != "postgres:5432" {
		t.Errorf("PostgresAddr = %q, want %q", cfg.PostgresAddr, "postgres:5432")
	}
}

func TestFromEnvPrefersExplicitEnvironmentValues(t *testing.T) {
	env := map[string]string{
		"ADS_HTTP_ADDR":    ":9999",
		"CERBOS_GRPC_ADDR": "pdp.internal:3593",
		"POSTGRES_ADDR":    "db.internal:5432",
	}

	cfg := config.FromEnv(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})

	if cfg.HTTPAddr != ":9999" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":9999")
	}
	if cfg.CerbosGRPCAddr != "pdp.internal:3593" {
		t.Errorf("CerbosGRPCAddr = %q, want %q", cfg.CerbosGRPCAddr, "pdp.internal:3593")
	}
	if cfg.PostgresAddr != "db.internal:5432" {
		t.Errorf("PostgresAddr = %q, want %q", cfg.PostgresAddr, "db.internal:5432")
	}
}
