package config_test

import (
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/resource-service/internal/config"
)

// The DSN carries a password, so it comes from the environment whole rather than
// being assembled here from parts. There is no useful default: a service that
// silently fell back to some built-in connection string would either fail late
// or, worse, reach a database nobody meant it to.
func TestTheResourceDatabaseDSNComesFromTheEnvironment(t *testing.T) {
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

func TestFromEnvFallsBackToTheComposeDefaults(t *testing.T) {
	cfg := config.FromEnv(func(string) (string, bool) { return "", false })

	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.ADSAddr != "http://ads:8080" {
		t.Errorf("ADSAddr = %q, want %q", cfg.ADSAddr, "http://ads:8080")
	}
	if cfg.PostgresAddr != "postgres:5432" {
		t.Errorf("PostgresAddr = %q, want %q", cfg.PostgresAddr, "postgres:5432")
	}
}

func TestFromEnvPrefersExplicitEnvironmentValues(t *testing.T) {
	env := map[string]string{
		"RESOURCE_SERVICE_HTTP_ADDR": ":9999",
		"ADS_ADDR":                   "http://ads.internal:8080",
		"POSTGRES_ADDR":              "db.internal:5432",
	}

	cfg := config.FromEnv(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})

	if cfg.HTTPAddr != ":9999" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":9999")
	}
	if cfg.ADSAddr != "http://ads.internal:8080" {
		t.Errorf("ADSAddr = %q, want %q", cfg.ADSAddr, "http://ads.internal:8080")
	}
	if cfg.PostgresAddr != "db.internal:5432" {
		t.Errorf("PostgresAddr = %q, want %q", cfg.PostgresAddr, "db.internal:5432")
	}
}
