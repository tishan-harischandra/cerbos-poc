// Package config resolves the ADS runtime configuration from the environment.
package config

// Config is the ADS runtime configuration.
type Config struct {
	HTTPAddr       string
	CerbosGRPCAddr string
	PostgresAddr   string
}

// LookupFunc mirrors os.LookupEnv so configuration stays testable.
type LookupFunc func(key string) (string, bool)

// FromEnv resolves the configuration, falling back to the docker compose
// service names used by the walking skeleton.
func FromEnv(lookup LookupFunc) Config {
	return Config{
		HTTPAddr:       valueOr(lookup, "ADS_HTTP_ADDR", ":8080"),
		CerbosGRPCAddr: valueOr(lookup, "CERBOS_GRPC_ADDR", "cerbos:3593"),
		PostgresAddr:   valueOr(lookup, "POSTGRES_ADDR", "postgres:5432"),
	}
}

func valueOr(lookup LookupFunc, key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}
