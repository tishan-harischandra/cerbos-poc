package config_test

import (
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/apps/policy-controller/internal/config"
)

func lookupFrom(values map[string]string) config.LookupFunc {
	return func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
}

func TestFromEnv_FallsBackToComposeDefaults(t *testing.T) {
	cfg := config.FromEnv(lookupFrom(nil))

	if cfg.GiteaBaseURL != "http://gitea:3000" {
		t.Fatalf("GiteaBaseURL = %q", cfg.GiteaBaseURL)
	}
	if cfg.GiteaRepo != "authz/root-policy" {
		t.Fatalf("GiteaRepo = %q", cfg.GiteaRepo)
	}
	if cfg.TagPrefix != "root-v" {
		t.Fatalf("TagPrefix = %q", cfg.TagPrefix)
	}
	if cfg.PollInterval != 30*time.Second {
		t.Fatalf("PollInterval = %v", cfg.PollInterval)
	}
	if cfg.RetainCount != 5 {
		t.Fatalf("RetainCount = %d", cfg.RetainCount)
	}
	if len(cfg.CerbosAdminAddresses) != 1 || cfg.CerbosAdminAddresses[0] != "cerbos:3593" {
		t.Fatalf("CerbosAdminAddresses = %v", cfg.CerbosAdminAddresses)
	}
}

func TestFromEnv_ReadsEveryConfiguredValue(t *testing.T) {
	cfg := config.FromEnv(lookupFrom(map[string]string{
		"POLICY_CONTROLLER_HTTP_ADDR":     ":9999",
		"GITEA_BASE_URL":                  "http://gitea.internal:3000",
		"GITEA_REPO":                      "authz/other-repo",
		"GITEA_TOKEN":                     "s3cr3t",
		"ROOT_POLICY_TAG_PREFIX":          "prod-",
		"POSTGRES_DSN":                    "postgres://u:p@postgres:5432/db",
		"CERBOS_ADMIN_ADDRESSES":          "cerbos-a:3593,cerbos-b:3593",
		"CERBOS_ADMIN_USERNAME":           "cerbos",
		"CERBOS_ADMIN_PASSWORD":           "hunter2",
		"POLICY_CONTROLLER_POLL_INTERVAL": "10s",
		"POLICY_CONTROLLER_RETAIN_COUNT":  "3",
		"POLICY_DIR":                      "/policies",
		"POLICY_ARCHIVE_STORE_DIR":        "/var/lib/policy-controller/archives",
		"POLICY_WORK_DIR":                 "/var/lib/policy-controller/work",
		"CERBOS_BINARY":                   "/usr/local/bin/cerbos",
	}))

	if cfg.HTTPAddr != ":9999" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.GiteaBaseURL != "http://gitea.internal:3000" {
		t.Fatalf("GiteaBaseURL = %q", cfg.GiteaBaseURL)
	}
	if cfg.GiteaRepo != "authz/other-repo" {
		t.Fatalf("GiteaRepo = %q", cfg.GiteaRepo)
	}
	if cfg.GiteaToken != "s3cr3t" {
		t.Fatalf("GiteaToken = %q", cfg.GiteaToken)
	}
	if cfg.TagPrefix != "prod-" {
		t.Fatalf("TagPrefix = %q", cfg.TagPrefix)
	}
	if cfg.PostgresDSN != "postgres://u:p@postgres:5432/db" {
		t.Fatalf("PostgresDSN = %q", cfg.PostgresDSN)
	}
	if len(cfg.CerbosAdminAddresses) != 2 || cfg.CerbosAdminAddresses[1] != "cerbos-b:3593" {
		t.Fatalf("CerbosAdminAddresses = %v", cfg.CerbosAdminAddresses)
	}
	if cfg.CerbosAdminUsername != "cerbos" || cfg.CerbosAdminPassword != "hunter2" {
		t.Fatalf("CerbosAdminUsername/Password = %q/%q", cfg.CerbosAdminUsername, cfg.CerbosAdminPassword)
	}
	if cfg.PollInterval != 10*time.Second {
		t.Fatalf("PollInterval = %v", cfg.PollInterval)
	}
	if cfg.RetainCount != 3 {
		t.Fatalf("RetainCount = %d", cfg.RetainCount)
	}
	if cfg.PolicyDir != "/policies" {
		t.Fatalf("PolicyDir = %q", cfg.PolicyDir)
	}
	if cfg.ArchiveStoreDir != "/var/lib/policy-controller/archives" {
		t.Fatalf("ArchiveStoreDir = %q", cfg.ArchiveStoreDir)
	}
	if cfg.WorkDir != "/var/lib/policy-controller/work" {
		t.Fatalf("WorkDir = %q", cfg.WorkDir)
	}
	if cfg.CerbosBinary != "/usr/local/bin/cerbos" {
		t.Fatalf("CerbosBinary = %q", cfg.CerbosBinary)
	}
}
