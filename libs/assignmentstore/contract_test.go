package assignmentstore_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/oraclestore"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/postgresstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/storecontract"
)

// The DSNs the contract suite runs against. An engine with no DSN is not
// exercised; see TestTheContractIsNotSilentlySkipped for why that cannot pass
// unnoticed in CI.
const (
	postgresDSNEnv = "ASSIGNMENTSTORE_POSTGRES_DSN"
	oracleDSNEnv   = "ASSIGNMENTSTORE_ORACLE_DSN"
)

func TestPostgreSQLSatisfiesTheStoreContract(t *testing.T) {
	dsn := os.Getenv(postgresDSNEnv)
	if dsn == "" {
		t.Skipf("%s is not set", postgresDSNEnv)
	}

	storecontract.Run(t, func(t *testing.T) assignmentstore.Store {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		store, err := postgresstore.Open(ctx, dsn)
		if err != nil {
			t.Fatalf("opening PostgreSQL: %v", err)
		}
		if err := store.Ping(ctx); err != nil {
			t.Fatalf("pinging PostgreSQL: %v", err)
		}
		return store
	})
}

func TestOracleSatisfiesTheStoreContract(t *testing.T) {
	dsn := os.Getenv(oracleDSNEnv)
	if dsn == "" {
		t.Skipf("%s is not set", oracleDSNEnv)
	}

	storecontract.Run(t, func(t *testing.T) assignmentstore.Store {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		store, err := oraclestore.Open(ctx, dsn)
		if err != nil {
			t.Fatalf("opening Oracle: %v", err)
		}
		if err := store.Ping(ctx); err != nil {
			t.Fatalf("pinging Oracle: %v", err)
		}
		return store
	})
}

// A skipped contract is indistinguishable from a passing one in a CI summary,
// and the portability claim rests entirely on this suite having run. So in CI,
// an engine without a DSN is a failure rather than a skip.
//
// REQUIRE_ENGINES names the engines that must be exercised, comma separated. The
// dual-dialect job sets it to "postgres,oracle"; a developer running the suite
// locally sets nothing and keeps the skips.
func TestTheContractIsNotSilentlySkipped(t *testing.T) {
	required := os.Getenv("REQUIRE_ENGINES")
	if required == "" {
		t.Skip("REQUIRE_ENGINES is not set, so skipping an engine is allowed here")
	}

	dsnFor := map[string]string{
		"postgres": os.Getenv(postgresDSNEnv),
		"oracle":   os.Getenv(oracleDSNEnv),
	}

	for _, engine := range strings.Split(required, ",") {
		engine = strings.TrimSpace(engine)
		if engine == "" {
			continue
		}
		dsn, known := dsnFor[engine]
		if !known {
			t.Errorf("REQUIRE_ENGINES names %q, which this suite cannot run", engine)
			continue
		}
		if dsn == "" {
			t.Errorf("the contract must run against %s here, but its DSN is empty", engine)
		}
	}
}
