// Package config resolves the ADS runtime configuration from the environment.
package config

import (
	"strconv"
	"strings"
	"time"
)

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
	// CapabilityCatalogDir is the local path the capability snapshot
	// endpoint reads UiCapabilityDefinitions from - the same
	// per-pod-mounted release tree Cerbos itself reads policies from
	// (§13.1, issue #11).
	CapabilityCatalogDir string
	// CapabilityCatalogRevision is the release's numeric catalog revision,
	// formatted into the §12.4 snapshot shape.
	CapabilityCatalogRevision int64
	// RootPolicyRevision is the immutable root-policy tag currently served
	// (§12.4, §13.1), e.g. "root-v1.4.0".
	RootPolicyRevision string

	// KafkaBrokers are the Kafka (or Redpanda) bootstrap addresses the
	// invalidation consumer reads PermissionChanged from (§10).
	KafkaBrokers []string
	// KafkaTopic is the topic the outbox publisher writes to and this
	// consumer reads from.
	KafkaTopic string
	// KafkaConsumerGroup is this replica's consumer group. Every ADS
	// replica shares one group so the topic's partitions are divided
	// between them rather than each replica reading every message.
	KafkaConsumerGroup string
	// ReconcileInterval bounds how often the revision reconciler compares
	// cached and actual tenant revisions (§10.3).
	ReconcileInterval time.Duration
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

		CapabilityCatalogDir:      valueOr(lookup, "CAPABILITY_CATALOG_DIR", "/etc/cerbos-catalog/ui-capabilities"),
		CapabilityCatalogRevision: int64Or(lookup, "CAPABILITY_CATALOG_REVISION", 1),
		RootPolicyRevision:        valueOr(lookup, "ROOT_POLICY_REVISION", "root-v1.4.0"),

		KafkaBrokers:       splitOr(lookup, "KAFKA_BROKERS", []string{"redpanda:9092"}),
		KafkaTopic:         valueOr(lookup, "KAFKA_PERMISSION_CHANGED_TOPIC", "permission-changed"),
		KafkaConsumerGroup: valueOr(lookup, "KAFKA_CONSUMER_GROUP", "ads"),
		ReconcileInterval:  durationOr(lookup, "ADS_RECONCILE_INTERVAL", 2*time.Second),
	}
}

// splitOr falls back on an unreadable value. The value is a comma-separated
// list, so a single broker needs no special-casing at the call site.
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

// int64Or falls back on an unreadable value, for the same reason
// durationOr does: a silent zero would be indistinguishable from a real
// revision zero rather than a misconfiguration.
func int64Or(lookup LookupFunc, key string, fallback int64) int64 {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
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
