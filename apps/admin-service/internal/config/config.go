// Package config resolves the Administration Service runtime configuration
// from the environment.
package config

// Config is the Administration Service runtime configuration.
type Config struct {
	HTTPAddr string
	// PostgresAddr is the host:port the readiness probe dials. It is
	// separate from the DSN because a readiness probe must not need a
	// credential.
	PostgresAddr string
	// PostgresDSN is the connection string the write path uses. It carries
	// a password, so it arrives whole from the environment rather than
	// being assembled from parts here.
	PostgresDSN string
	// IdPAddr is the host:port the readiness probe dials.
	IdPAddr string
	// CatalogDir is the local path the active resource/action catalog is
	// loaded from - the same per-pod-mounted release tree Cerbos and the
	// ADS read policies and UI capabilities from (§13.1).
	CatalogDir string
}

// LookupFunc mirrors os.LookupEnv so configuration stays testable.
type LookupFunc func(key string) (string, bool)

// FromEnv resolves the configuration, falling back to the docker compose
// service names used by the walking skeleton.
func FromEnv(lookup LookupFunc) Config {
	return Config{
		HTTPAddr:     valueOr(lookup, "ADMIN_SERVICE_HTTP_ADDR", ":8081"),
		PostgresAddr: valueOr(lookup, "POSTGRES_ADDR", "postgres:5432"),
		PostgresDSN:  valueOr(lookup, "ASSIGNMENTSTORE_POSTGRES_DSN", ""),
		IdPAddr:      valueOr(lookup, "IDP_ADDR", "keycloak:8080"),
		CatalogDir:   valueOr(lookup, "AUTHORIZATION_CATALOG_DIR", "/etc/cerbos-catalog/resources"),
	}
}

func valueOr(lookup LookupFunc, key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}
