package redislock_test

import (
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/leaderlockcontract"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/redislock"
)

// Redis is optional infrastructure, so the contract runs where it is
// available and is skipped where it is not - the same arrangement the Oracle
// store contract uses.
const redisAddressEnv = "LEADER_ELECTION_REDIS_ADDR"

const leaseTTL = 2 * time.Second

func TestRedisSatisfiesTheLeaderElectionContract(t *testing.T) {
	address := os.Getenv(redisAddressEnv)
	if address == "" {
		t.Skipf("%s is not set", redisAddressEnv)
	}

	// A prefix per run, so a re-run does not contend with the leases a
	// previous one left behind.
	prefix := "leaderlock-contract/" + strconv.FormatInt(time.Now().UnixNano(), 36) + "/"

	identities := 0
	leaderlockcontract.Run(t, leaderlockcontract.Contract{
		NewContender: func(t *testing.T) leaderlockcontract.Contender {
			t.Helper()
			identities++
			pause := make(chan struct{})
			elector, err := redislock.New(redislock.Config{
				Address:       address,
				Password:      os.Getenv("LEADER_ELECTION_REDIS_PASSWORD"),
				KeyPrefix:     prefix,
				Identity:      "contender-" + strconv.Itoa(identities),
				TTL:           leaseTTL,
				RenewInterval: leaseTTL / 4,
				RetryInterval: 200 * time.Millisecond,
				PauseRenewal:  pause,
			})
			if err != nil {
				t.Fatalf("redislock.New: %v", err)
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

func TestAnAddressIsRequired(t *testing.T) {
	_, err := redislock.New(redislock.Config{Identity: "replica-a", TTL: leaseTTL})
	if err == nil {
		t.Error("New accepted an elector with nowhere to connect")
	}
}

func TestALeaseWithNoTTLIsRefused(t *testing.T) {
	_, err := redislock.New(redislock.Config{Address: "redis:6379", Identity: "replica-a"})
	if err == nil {
		t.Error("New accepted a lease that never expires")
	}
}

// Two installations sharing one Redis must not share one election, and the
// prefix is the only thing keeping them apart.
func TestElectionsAreNamespacedByDefault(t *testing.T) {
	if redislock.DefaultKeyPrefix == "" {
		t.Error("elections are written to unprefixed keys, so two installations would contend for one lock")
	}
}

var _ leaderlock.Elector = (*redislock.Elector)(nil)
