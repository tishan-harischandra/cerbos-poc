// Package config resolves the Administration Service runtime configuration
// from the environment.
package config

import (
	"strings"
	"time"
)

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
	// CapabilityCatalogDir is the local path the capability impact
	// endpoint (issue #18) reads UiCapabilityDefinitions from. Mirrors
	// the ADS's own config field of the same name and default.
	CapabilityCatalogDir string
	// ADSAddr is the ADS's base address the simulator (issue #19) calls
	// its internal simulation endpoints against, e.g. "http://ads:8080".
	ADSAddr string
	// RootPolicyRevision is the immutable root-policy tag currently served
	// (§9.4's "current root revision"), e.g. "root-v1.4.0". Mirrors the
	// ADS's own config field of the same name and default.
	RootPolicyRevision string
	// PolicyReleaseStoreDir is where the policy controller's archives and
	// release history live (issue #22's revision and activation module). It
	// is only ever populated when the policy-release compose profile is
	// running the real controller (issue #21); reading an empty directory
	// simply reports no current revision and no history, never an error.
	PolicyReleaseStoreDir string

	// KafkaBrokers are the Kafka (or Redpanda) bootstrap addresses the
	// outbox publisher loop writes PermissionChanged to (§10).
	KafkaBrokers []string
	// KafkaTopic is the topic the publisher writes to and the ADS reads
	// from.
	KafkaTopic string
	// OutboxPublishInterval bounds how often the publisher loop polls for
	// unpublished outbox rows.
	OutboxPublishInterval time.Duration

	// HighRiskActions names the action keys the user-override write path
	// (§9.3, issue #15) defaults to a bounded expiry when a GRANT or
	// REVOKE names none.
	HighRiskActions []string
	// HighRiskOverrideValidity bounds a high-risk GRANT or REVOKE that
	// names no ValidUntil. Non-positive falls back to
	// useroverride.DefaultHighRiskValidityWindow.
	HighRiskOverrideValidity time.Duration

	// ConsoleDir is the built Admin Console bundle this service serves
	// (ADR-008). Empty means it serves the administration API only, which
	// is what a build with no bundle on disk - a developer running
	// `nx serve admin-console` against it - wants.
	ConsoleDir string
}

// LookupFunc mirrors os.LookupEnv so configuration stays testable.
type LookupFunc func(key string) (string, bool)

// FromEnv resolves the configuration, falling back to the docker compose
// service names used by the walking skeleton.
func FromEnv(lookup LookupFunc) Config {
	return Config{
		HTTPAddr:              valueOr(lookup, "ADMIN_SERVICE_HTTP_ADDR", ":8081"),
		PostgresAddr:          valueOr(lookup, "POSTGRES_ADDR", "postgres:5432"),
		PostgresDSN:           valueOr(lookup, "ASSIGNMENTSTORE_POSTGRES_DSN", ""),
		IdPAddr:               valueOr(lookup, "IDP_ADDR", "keycloak:8080"),
		CatalogDir:            valueOr(lookup, "AUTHORIZATION_CATALOG_DIR", "/etc/cerbos-catalog/resources"),
		CapabilityCatalogDir:  valueOr(lookup, "CAPABILITY_CATALOG_DIR", "/etc/cerbos-catalog/ui-capabilities"),
		ADSAddr:               valueOr(lookup, "ADS_ADDR", "http://ads:8080"),
		RootPolicyRevision:    valueOr(lookup, "ROOT_POLICY_REVISION", "root-v1.4.0"),
		PolicyReleaseStoreDir: valueOr(lookup, "POLICY_ARCHIVE_STORE_DIR", ""),

		KafkaBrokers:          splitOr(lookup, "KAFKA_BROKERS", []string{"redpanda:9092"}),
		KafkaTopic:            valueOr(lookup, "KAFKA_PERMISSION_CHANGED_TOPIC", "permission-changed"),
		OutboxPublishInterval: durationOr(lookup, "OUTBOX_PUBLISH_INTERVAL", 2*time.Second),

		HighRiskActions:          splitOr(lookup, "USER_OVERRIDE_HIGH_RISK_ACTIONS", []string{"update", "delete", "assign"}),
		HighRiskOverrideValidity: durationOr(lookup, "USER_OVERRIDE_HIGH_RISK_VALIDITY", 90*24*time.Hour),

		ConsoleDir: valueOr(lookup, "ADMIN_CONSOLE_DIR", "/admin-console"),
	}
}

func valueOr(lookup LookupFunc, key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}

func splitOr(lookup LookupFunc, key string, fallback []string) []string {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback
	}
	parts := strings.Split(value, ",")
	trimmed := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			trimmed = append(trimmed, part)
		}
	}
	if len(trimmed) == 0 {
		return fallback
	}
	return trimmed
}

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
