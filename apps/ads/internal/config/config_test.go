package config_test

import (
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/config"
)

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
