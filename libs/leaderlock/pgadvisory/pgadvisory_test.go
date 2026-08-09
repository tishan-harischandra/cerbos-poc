package pgadvisory_test

import (
	"os"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/leaderlockcontract"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/pgadvisory"
)

// The elector shares the authorization database, so it shares its DSN.
const postgresDSNEnv = "ASSIGNMENTSTORE_POSTGRES_DSN"

func TestPgAdvisorySatisfiesTheLeaderElectionContract(t *testing.T) {
	dsn := os.Getenv(postgresDSNEnv)
	if dsn == "" {
		t.Skipf("%s is not set", postgresDSNEnv)
	}

	leaderlockcontract.Run(t, leaderlockcontract.Contract{
		NewContender: func(t *testing.T) leaderlockcontract.Contender {
			t.Helper()
			elector, err := pgadvisory.New(pgadvisory.Config{
				DSN:           dsn,
				CheckInterval: 200 * time.Millisecond,
				RetryInterval: 100 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("pgadvisory.New: %v", err)
			}
			return leaderlockcontract.Contender{Elector: elector}
		},
		// No ttl: the lock lives on the session, so it is never
		// renewed and never expires. That is the guarantee this
		// adapter exists to provide, and declaring it here is what
		// keeps the renewal cases from pretending to cover it.
		TTL: 0,
	})
}

func TestADSNIsRequired(t *testing.T) {
	if _, err := pgadvisory.New(pgadvisory.Config{}); err == nil {
		t.Error("New accepted an elector with nowhere to connect")
	}
}

var _ leaderlock.Elector = (*pgadvisory.Elector)(nil)
