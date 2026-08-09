package databaselock_test

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/databaselock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/leaderlockcontract"
)

// The election shares the authorization database, so it shares its DSNs.
const (
	postgresDSNEnv = "ASSIGNMENTSTORE_POSTGRES_DSN"
	oracleDSNEnv   = "ASSIGNMENTSTORE_ORACLE_DSN"
)

// leaseTTL is short so the expiry cases finish quickly, and still long
// enough that a slow query does not depose a healthy leader.
const leaseTTL = 2 * time.Second

func TestDatabaseLockSatisfiesTheContractOnPostgreSQL(t *testing.T) {
	dsn := os.Getenv(postgresDSNEnv)
	if dsn == "" {
		t.Skipf("%s is not set", postgresDSNEnv)
	}
	runContract(t, dsn)
}

// The same assertions, the other engine. This is the case that makes the
// portability claim: an installation on the oracle profile gets leader
// election with the same behaviour, not a different one.
func TestDatabaseLockSatisfiesTheContractOnOracle(t *testing.T) {
	dsn := os.Getenv(oracleDSNEnv)
	if dsn == "" {
		t.Skipf("%s is not set", oracleDSNEnv)
	}
	runContract(t, dsn)
}

// A skipped contract and a passing one look identical in a CI summary, and
// the dual-dialect claim rests entirely on this suite having run against
// both engines.
func TestTheContractIsNotSilentlySkipped(t *testing.T) {
	required := os.Getenv("REQUIRE_ENGINES")
	if required == "" {
		t.Skip("REQUIRE_ENGINES is not set, so skipping an engine is allowed here")
	}
	for _, engine := range strings.Split(required, ",") {
		switch engine = strings.TrimSpace(engine); engine {
		case "":
		case "postgres":
			if os.Getenv(postgresDSNEnv) == "" {
				t.Errorf("%s is required but %s is not set", engine, postgresDSNEnv)
			}
		case "oracle":
			if os.Getenv(oracleDSNEnv) == "" {
				t.Errorf("%s is required but %s is not set", engine, oracleDSNEnv)
			}
		default:
			t.Errorf("REQUIRE_ENGINES names %q, which is not an engine this suite knows", engine)
		}
	}
}

func TestTheDialectIsInferredFromTheDSNScheme(t *testing.T) {
	cases := map[string]databaselock.Dialect{
		"oracle://cerbos_poc:pw@oracle:1521/FREEPDB1": databaselock.DialectOracle,
		"postgres://cerbos_poc:pw@postgres:5432/db":   databaselock.DialectPostgres,
		"host=postgres user=cerbos_poc dbname=db":     databaselock.DialectPostgres,
	}
	for dsn, want := range cases {
		if got := databaselock.DialectFor(dsn); got != want {
			t.Errorf("DialectFor(%q) = %q, want %q", dsn, got, want)
		}
	}
}

// A lease with no ttl never expires, so a leader that dies holds the election
// until somebody notices by hand. Refusing at construction is the only place
// that failure is cheap to see.
func TestALeaseWithNoTTLIsRefused(t *testing.T) {
	_, err := databaselock.New(databaselock.Config{
		DSN:      "postgres://cerbos_poc@postgres:5432/db",
		Identity: "replica-a",
	})
	if err == nil {
		t.Error("New accepted a lease that never expires")
	}
}

func TestALeaseWithNoHolderIsRefused(t *testing.T) {
	_, err := databaselock.New(databaselock.Config{
		DSN: "postgres://cerbos_poc@postgres:5432/db",
		TTL: leaseTTL,
	})
	if err == nil {
		t.Error("New accepted a lease that names nobody")
	}
}

func runContract(t *testing.T, dsn string) {
	t.Helper()

	// Each contender is a distinct holder, exactly as two replicas are.
	// Sharing an identity would let one renew the other's lease and the
	// contention cases would pass for the wrong reason.
	identities := 0

	leaderlockcontract.Run(t, leaderlockcontract.Contract{
		NewContender: func(t *testing.T) leaderlockcontract.Contender {
			t.Helper()
			identities++
			pause := make(chan struct{})
			elector, err := databaselock.New(databaselock.Config{
				DSN:           dsn,
				Identity:      contenderName(identities),
				TTL:           leaseTTL,
				RenewInterval: leaseTTL / 4,
				RetryInterval: 200 * time.Millisecond,
				PauseRenewal:  pause,
			})
			if err != nil {
				t.Fatalf("databaselock.New: %v", err)
			}
			t.Cleanup(func() { _ = elector.Close() })
			return leaderlockcontract.Contender{
				Elector: elector,
				Stall:   sync.OnceFunc(func() { close(pause) }),
			}
		},
		TTL: leaseTTL,
	})
}

func contenderName(n int) string {
	return "contract-contender-" + strconv.Itoa(n)
}

var _ leaderlock.Elector = (*databaselock.Elector)(nil)
