// Package config resolves the policy controller's runtime configuration from
// the environment.
package config

import (
	"strconv"
	"strings"
	"time"
)

// Config is the policy controller's runtime configuration.
type Config struct {
	HTTPAddr string

	GiteaBaseURL string
	GiteaRepo    string
	GiteaToken   string
	TagPrefix    string

	// PostgresDSN backs the leader-election advisory lock. It is not the
	// assignment data connection: this controller never reads or writes
	// assignment data (§13.1).
	PostgresDSN string

	CerbosAdminAddresses []string
	CerbosAdminUsername  string
	CerbosAdminPassword  string
	CerbosAdminPlaintext bool

	// CerbosBinary is the cerbos executable the validation gate shells out
	// to for `compile --tests=...` (§13.2).
	CerbosBinary string

	// PolicyDir is this replica's local policy directory - the same path
	// Cerbos's disk storage driver serves from.
	PolicyDir string
	// ArchiveStoreDir retains built archives and manifests (issue #21).
	ArchiveStoreDir string
	// WorkDir is scratch space for extracting a fetched commit.
	WorkDir string

	PollInterval time.Duration
	RetainCount  int
}

// LookupFunc mirrors os.LookupEnv so configuration stays testable.
type LookupFunc func(key string) (string, bool)

// FromEnv resolves the configuration, falling back to the docker compose
// service names and paths this repository's compose topology uses.
func FromEnv(lookup LookupFunc) Config {
	return Config{
		HTTPAddr: valueOr(lookup, "POLICY_CONTROLLER_HTTP_ADDR", ":8082"),

		GiteaBaseURL: valueOr(lookup, "GITEA_BASE_URL", "http://gitea:3000"),
		GiteaRepo:    valueOr(lookup, "GITEA_REPO", "authz/root-policy"),
		GiteaToken:   valueOr(lookup, "GITEA_TOKEN", ""),
		TagPrefix:    valueOr(lookup, "ROOT_POLICY_TAG_PREFIX", "root-v"),

		PostgresDSN: valueOr(lookup, "POSTGRES_DSN", ""),

		CerbosAdminAddresses: splitOr(lookup, "CERBOS_ADMIN_ADDRESSES", []string{"cerbos:3593"}),
		CerbosAdminUsername:  valueOr(lookup, "CERBOS_ADMIN_USERNAME", "cerbos"),
		CerbosAdminPassword:  valueOr(lookup, "CERBOS_ADMIN_PASSWORD", ""),
		CerbosAdminPlaintext: boolOr(lookup, "CERBOS_ADMIN_PLAINTEXT", true),

		CerbosBinary: valueOr(lookup, "CERBOS_BINARY", "cerbos"),

		PolicyDir:       valueOr(lookup, "POLICY_DIR", "/policies"),
		ArchiveStoreDir: valueOr(lookup, "POLICY_ARCHIVE_STORE_DIR", "/var/lib/policy-controller/archives"),
		WorkDir:         valueOr(lookup, "POLICY_WORK_DIR", "/var/lib/policy-controller/work"),

		PollInterval: durationOr(lookup, "POLICY_CONTROLLER_POLL_INTERVAL", 30*time.Second),
		RetainCount:  intOr(lookup, "POLICY_CONTROLLER_RETAIN_COUNT", 5),
	}
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

func intOr(lookup LookupFunc, key string, fallback int) int {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func boolOr(lookup LookupFunc, key string, fallback bool) bool {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
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

func valueOr(lookup LookupFunc, key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}
