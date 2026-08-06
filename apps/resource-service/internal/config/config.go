// Package config resolves the resource service's runtime configuration from
// the environment.
package config

// Config is the resource service's runtime configuration.
type Config struct {
	HTTPAddr string
	// ADSAddr is the Assignment Data Service's base URL. The resource
	// service is a PEP that asks the ADS for a decision (issue #9); it never
	// calls the PDP directly, so there is no Cerbos address here.
	ADSAddr string
	// PostgresAddr is the host:port the readiness probe dials. Separate from
	// the DSN so a readiness probe never needs a credential.
	PostgresAddr string
	// PostgresDSN is the connection string the resource store reads and
	// writes fhir_resource through.
	PostgresDSN string
}

// LookupFunc mirrors os.LookupEnv so configuration stays testable.
type LookupFunc func(key string) (string, bool)

// FromEnv resolves the configuration, falling back to the docker compose
// service names used by the walking skeleton.
func FromEnv(lookup LookupFunc) Config {
	return Config{
		HTTPAddr:     valueOr(lookup, "RESOURCE_SERVICE_HTTP_ADDR", ":8080"),
		ADSAddr:      valueOr(lookup, "ADS_ADDR", "http://ads:8080"),
		PostgresAddr: valueOr(lookup, "POSTGRES_ADDR", "postgres:5432"),
		PostgresDSN:  valueOr(lookup, "ASSIGNMENTSTORE_POSTGRES_DSN", ""),
	}
}

func valueOr(lookup LookupFunc, key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}
