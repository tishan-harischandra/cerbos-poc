// Package config resolves the ADS runtime configuration from the environment.
package config

import "time"

// DefaultRoleMatrixCacheTTL bounds how long the in-process role matrix cache may
// outlive a change to the underlying row (§11.2). It is short because expiry is
// the only invalidation mechanism until the outbox drives targeted invalidation.
const DefaultRoleMatrixCacheTTL = 30 * time.Second

// Config is the ADS runtime configuration.
type Config struct {
	HTTPAddr       string
	CerbosGRPCAddr string
	// PostgresAddr is the host:port the readiness probe dials. It is separate
	// from the DSN because a readiness probe must not need a credential.
	PostgresAddr string
	// PostgresDSN is the connection string the decision path reads the role
	// matrix through. It carries a password, so it arrives whole from the
	// environment rather than being assembled from parts here.
	PostgresDSN        string
	RoleMatrixCacheTTL time.Duration
	// IdPAddr is the host:port the readiness probe dials. Like PostgresAddr it
	// carries no credential, because a readiness probe must not need one.
	IdPAddr string
}

// LookupFunc mirrors os.LookupEnv so configuration stays testable.
type LookupFunc func(key string) (string, bool)

// FromEnv resolves the configuration, falling back to the docker compose
// service names used by the walking skeleton.
func FromEnv(lookup LookupFunc) Config {
	return Config{
		HTTPAddr:           valueOr(lookup, "ADS_HTTP_ADDR", ":8080"),
		CerbosGRPCAddr:     valueOr(lookup, "CERBOS_GRPC_ADDR", "cerbos:3593"),
		PostgresAddr:       valueOr(lookup, "POSTGRES_ADDR", "postgres:5432"),
		PostgresDSN:        valueOr(lookup, "ASSIGNMENTSTORE_POSTGRES_DSN", ""),
		RoleMatrixCacheTTL: durationOr(lookup, "ADS_ROLE_MATRIX_CACHE_TTL", DefaultRoleMatrixCacheTTL),
		IdPAddr:            valueOr(lookup, "IDP_ADDR", "keycloak:8080"),
	}
}

// durationOr falls back on an unreadable value rather than on zero. A zero
// lifetime would disable the cache silently and put the database on the hot path
// of every decision, which is a performance cliff nobody would think to look for
// in a typo.
func durationOr(lookup LookupFunc, key string, fallback time.Duration) time.Duration {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func valueOr(lookup LookupFunc, key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}
