// These are internal tests. The adapter's whole substance is its use of the
// API server's optimistic concurrency, and a case that pins that has to be
// able to put one contender into a known state without driving a whole
// election to get there.
package k8slease

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/leaderlockcontract"
)

// leaseTTL is a whole second because leaseDurationSeconds is an integer
// field; anything shorter would be rounded and the case would be testing the
// rounding rather than the lease.
const leaseTTL = time.Second

const serviceAccountToken = "a-service-account-token"

func TestK8sLeaseSatisfiesTheLeaderElectionContract(t *testing.T) {
	api := newFakeAPIServer(t, serviceAccountToken)

	identities := 0
	leaderlockcontract.Run(t, leaderlockcontract.Contract{
		NewContender: func(t *testing.T) leaderlockcontract.Contender {
			t.Helper()
			identities++
			pause := make(chan struct{})
			elector, err := New(Config{
				BaseURL:       api.url(),
				Namespace:     "cerbos-poc",
				Token:         serviceAccountToken,
				HTTPClient:    api.server.Client(),
				Identity:      "contender-" + strconv.Itoa(identities),
				TTL:           leaseTTL,
				RenewInterval: 200 * time.Millisecond,
				RetryInterval: 100 * time.Millisecond,
				PauseRenewal:  pause,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			return leaderlockcontract.Contender{
				Elector: elector,
				Stall:   sync.OnceFunc(func() { close(pause) }),
			}
		},
		TTL: leaseTTL,
	})
}

// This adapter's exclusion is optimistic concurrency and nothing else: two
// contenders that read the same Lease are separated only by the API server
// rejecting the second write. A rejection is therefore an ordinary outcome -
// "somebody got there first" - and must not surface as a failure, or a
// contender would report an outage every time it simply lost.
func TestARejectedWriteMeansSomebodyElseWonRatherThanAnOutage(t *testing.T) {
	api := newFakeAPIServer(t, serviceAccountToken)
	elector := newContender(t, api, "loser")

	api.rejectNextWrite()

	acquired, err := elector.Acquire(context.Background())
	if err != nil {
		t.Fatalf("a rejected write was reported as an error: %v", err)
	}
	if acquired {
		t.Error("the contender believed it took a Lease the API server refused to write")
	}
	if api.conflictCount() != 1 {
		t.Errorf("the API server rejected %d writes, want the case to have exercised the rejection", api.conflictCount())
	}

	// And it recovers: the next attempt, unobstructed, wins.
	acquired, err = elector.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire after a rejected write: %v", err)
	}
	if !acquired {
		t.Error("a contender that lost one race never took the free Lease afterwards")
	}
}

// A leader that shuts down politely clears the holder and backdates the
// Lease, precisely so the fleet does not have to wait out a duration nobody is
// using. A successor that insisted on watching the record stand still for a
// whole lease first would throw that away and turn every rolling restart into
// a ttl of no leader at all.
func TestAReleasedLeaseIsTakenOverAtOnce(t *testing.T) {
	api := newFakeAPIServer(t, serviceAccountToken)
	outgoing := newContender(t, api, "outgoing")
	successor := newContender(t, api, "successor")
	ctx := context.Background()

	acquired, err := outgoing.Acquire(ctx)
	if err != nil || !acquired {
		t.Fatalf("Acquire by the first contender = %t, %v", acquired, err)
	}
	if err := outgoing.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// One attempt, with no waiting: the Lease is explicitly free, and
	// nothing about it needs to be observed over time to know that.
	acquired, err = successor.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire by the successor: %v", err)
	}
	if !acquired {
		t.Error("a Lease its holder had released was not taken over until it expired")
	}
}

// A pod that forgot its ServiceAccount token gets 401 from every call, and
// the loop must report that rather than treat it as "somebody else leads".
func TestAnUnauthenticatedContenderIsNeverElected(t *testing.T) {
	api := newFakeAPIServer(t, serviceAccountToken)

	elector, err := New(Config{
		BaseURL:       api.url(),
		Namespace:     "cerbos-poc",
		Token:         "the-wrong-token",
		HTTPClient:    api.server.Client(),
		Identity:      "unauthenticated",
		TTL:           leaseTTL,
		RenewInterval: 50 * time.Millisecond,
		RetryInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := contextWithTimeout(2 * time.Second)
	defer cancel()

	elected := make(chan struct{})
	go func() {
		_ = elector.Run(ctx, leaderlock.ElectionPolicyController, func(context.Context) { close(elected) })
	}()

	select {
	case <-elected:
		t.Fatal("a contender the API server rejected was elected anyway")
	case <-time.After(500 * time.Millisecond):
	}
}

func TestAnIdentityIsRequired(t *testing.T) {
	_, err := New(Config{BaseURL: "https://kubernetes.default.svc", Namespace: "n", Token: "t", TTL: leaseTTL})
	if err == nil {
		t.Error("New accepted a Lease that names nobody")
	}
}

// newContender builds one contender already pointed at an election, which is
// what Run would otherwise do on its way into the campaign loop.
func newContender(t *testing.T, api *fakeAPIServer, identity string) *Elector {
	t.Helper()
	elector, err := New(Config{
		BaseURL:       api.url(),
		Namespace:     "cerbos-poc",
		Token:         serviceAccountToken,
		HTTPClient:    api.server.Client(),
		Identity:      identity,
		TTL:           leaseTTL,
		RenewInterval: 50 * time.Millisecond,
		RetryInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	elector.election = leaderlock.ElectionPolicyController
	return elector
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

var _ leaderlock.Elector = (*Elector)(nil)
