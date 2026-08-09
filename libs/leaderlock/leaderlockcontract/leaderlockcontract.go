// Package leaderlockcontract is the one behavioural suite every leader
// election adapter must pass.
//
// It exists because the promise the port makes - "pick a backend with an
// environment variable" - is only true if the backends behave the same. Each
// adapter's own test file supplies a way to build contenders and then runs
// this suite; nothing about an election's semantics is asserted anywhere else.
//
// An adapter declares what it cannot do through Contract's capability fields
// rather than by omitting a test, so a case is skipped only where the skip is
// written down and justified.
package leaderlockcontract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
)

// Contender is one independent participant in an election, as an adapter's
// test knows how to build it. Two Contenders from the same Contract must
// contend with each other exactly as two processes would.
type Contender struct {
	Elector leaderlock.Elector

	// Stall makes this contender stop renewing for longer than its lease
	// lives and then resume, reproducing a leader whose process was paused
	// or whose network dropped and which then came back.
	//
	// A lease adapter must supply it: without it there is no way to prove
	// the lease actually expires, only that a polite release works. Leave
	// it nil only when the adapter has no lease to expire.
	Stall func()
}

// Contract describes one adapter to the suite.
type Contract struct {
	// NewContender builds a contender for the election the suite runs.
	// Every contender a single Run produces contends for the same
	// election, and contenders from different Runs must not.
	NewContender func(t *testing.T) Contender

	// TTL is how long this adapter's lease survives without renewal. Zero
	// means the adapter holds leadership for as long as its session lives
	// and never renews, which is true of PG_ADVISORY and of SINGLE.
	TTL time.Duration

	// AlwaysLeader marks an adapter that performs no coordination at all
	// and elects every caller. Only SINGLE may set it.
	AlwaysLeader bool
}

// Timeout bounds how long a case waits for an election to resolve before
// calling it a failure. It is generous: a case that needs most of it is
// already telling us something.
const Timeout = 20 * time.Second

// Run executes the whole contract against one adapter.
func Run(t *testing.T, c Contract) {
	t.Helper()
	if c.NewContender == nil {
		t.Fatal("the contract needs a way to build a contender")
	}
	if c.TTL > 0 && !c.AlwaysLeader {
		if probe := c.NewContender(t); probe.Stall == nil {
			t.Fatal("a lease-based adapter must supply Stall, or its lease expiry is never proven")
		}
	}

	t.Run("an uncontended contender is elected", func(t *testing.T) {
		runElectionIsWon(t, c)
	})
	t.Run("shutting down cancels the leader context", func(t *testing.T) {
		runShutdownCancelsLeaderContext(t, c)
	})
	t.Run("exactly one of two contenders leads", func(t *testing.T) {
		if c.AlwaysLeader {
			t.Skip("this adapter elects every caller by design and coordinates with nobody")
		}
		runExactlyOneLeader(t, c)
	})
	t.Run("a renewing leader keeps leadership past the ttl", func(t *testing.T) {
		if c.AlwaysLeader || c.TTL == 0 {
			t.Skip("this adapter holds leadership without renewing, so there is no ttl to outlive")
		}
		runRenewalOutlivesTheTTL(t, c)
	})
	t.Run("a leader that stalls past its lease loses the election to its rival", func(t *testing.T) {
		if c.AlwaysLeader || c.TTL == 0 {
			t.Skip("this adapter has no lease to expire; a vanished holder is detected by its session ending")
		}
		runLostLeadershipPassesToTheRival(t, c)
	})
}

func runElectionIsWon(t *testing.T, c Contract) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	elected := make(chan struct{})
	run := start(t, ctx, c.NewContender(t).Elector, func(context.Context) { close(elected) })

	select {
	case <-elected:
	case <-ctx.Done():
		t.Fatal("no contender was elected, though nobody was contending")
	}
	cancel()
	awaitRun(t, run)
}

// A caller's work must stop when the process is shutting down, not merely be
// abandoned: an outbox drain still writing while its service exits is how
// half-finished work escapes.
func runShutdownCancelsLeaderContext(t *testing.T, c Contract) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	leaderContexts := make(chan context.Context, 1)
	run := start(t, ctx, c.NewContender(t).Elector, func(leaderCtx context.Context) {
		leaderContexts <- leaderCtx
		<-leaderCtx.Done()
	})

	var leaderCtx context.Context
	select {
	case leaderCtx = <-leaderContexts:
	case <-ctx.Done():
		t.Fatal("no contender was elected")
	}

	cancel()
	select {
	case <-leaderCtx.Done():
	case <-time.After(Timeout):
		t.Fatal("the leader context outlived the shutdown of the election it came from")
	}
	awaitRun(t, run)
}

// The whole point of the port. Two contenders, one election, and the work must
// not be running twice.
func runExactlyOneLeader(t *testing.T, c Contract) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	first := make(chan struct{})
	second := make(chan struct{})
	runFirst := start(t, ctx, c.NewContender(t).Elector, holdUntilDone(closeOnce(first)))
	runSecond := start(t, ctx, c.NewContender(t).Elector, holdUntilDone(closeOnce(second)))

	var winner chan struct{}
	var loser chan struct{}
	select {
	case <-first:
		winner, loser = first, second
	case <-second:
		winner, loser = second, first
	case <-ctx.Done():
		t.Fatal("neither contender was elected")
	}
	_ = winner

	// A rival that is merely slow looks identical to a rival that was
	// correctly kept out, so the only honest check is to wait.
	select {
	case <-loser:
		t.Fatal("both contenders were elected at once")
	case <-time.After(settleFor(c)):
	}

	cancel()
	awaitRun(t, runFirst)
	awaitRun(t, runSecond)
}

// A leader that is alive and renewing must not be deposed just because time
// passed. An adapter whose renewal is broken passes every other case in this
// suite and then hands the election to a rival in production.
func runRenewalOutlivesTheTTL(t *testing.T, c Contract) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	leaderContexts := make(chan context.Context, 1)
	run := start(t, ctx, c.NewContender(t).Elector, func(leaderCtx context.Context) {
		leaderContexts <- leaderCtx
		<-leaderCtx.Done()
	})

	var leaderCtx context.Context
	select {
	case leaderCtx = <-leaderContexts:
	case <-ctx.Done():
		t.Fatal("no contender was elected")
	}

	select {
	case <-leaderCtx.Done():
		t.Fatalf("leadership ended after less than %s while the leader was still renewing: %v",
			3*c.TTL, context.Cause(leaderCtx))
	case <-time.After(3 * c.TTL):
	}

	cancel()
	awaitRun(t, run)
}

// The failure a lease exists to survive: a leader goes quiet for longer than
// its lease without releasing anything. The lease must expire, the rival must
// take over, and when the stalled leader speaks again its work must be
// cancelled with a cause that says why - not carried on as though the pause
// had never happened.
func runLostLeadershipPassesToTheRival(t *testing.T, c Contract) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	type election struct {
		contender Contender
		leaderCtx chan context.Context
	}
	elections := make([]*election, 2)
	runs := make([]<-chan error, 2)
	for i := range elections {
		e := &election{contender: c.NewContender(t), leaderCtx: make(chan context.Context, 4)}
		elections[i] = e
		runs[i] = start(t, ctx, e.contender.Elector, func(leaderCtx context.Context) {
			e.leaderCtx <- leaderCtx
			<-leaderCtx.Done()
		})
	}

	var leader, rival *election
	var leaderCtx context.Context
	select {
	case leaderCtx = <-elections[0].leaderCtx:
		leader, rival = elections[0], elections[1]
	case leaderCtx = <-elections[1].leaderCtx:
		leader, rival = elections[1], elections[0]
	case <-ctx.Done():
		t.Fatal("neither contender was elected")
	}

	leader.contender.Stall()

	select {
	case <-leaderCtx.Done():
	case <-time.After(expiryPatience(c)):
		t.Fatalf("a leader that stopped renewing still held leadership %s after its lease should have expired", expiryPatience(c))
	}
	// Cancelled is not enough: a caller distinguishes "we are shutting
	// down" from "somebody else is the leader now" only by the cause.
	if cause := context.Cause(leaderCtx); !errors.Is(cause, leaderlock.ErrLeadershipLost) {
		t.Errorf("the deposed leader's context was cancelled with %v, want ErrLeadershipLost", cause)
	}

	select {
	case <-rival.leaderCtx:
	case <-time.After(expiryPatience(c)):
		t.Fatal("the rival never took over an election whose leader had stopped renewing")
	}

	cancel()
	for _, run := range runs {
		awaitRun(t, run)
	}
}

// settleFor is how long a case waits before concluding a rival stayed out.
// Long enough to cover a lease turning over, since an adapter that lets a
// second leader in usually does so at the first renewal boundary.
func settleFor(c Contract) time.Duration {
	if c.TTL > 0 {
		return 2 * c.TTL
	}
	return 2 * time.Second
}

// expiryPatience allows a lease to expire plus a poll interval for the rival
// to notice, with room for a slow CI machine.
func expiryPatience(c Contract) time.Duration {
	patience := 4 * c.TTL
	if patience < 5*time.Second {
		patience = 5 * time.Second
	}
	return patience
}

func start(t *testing.T, ctx context.Context, elector leaderlock.Elector, onElected func(context.Context)) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- elector.Run(ctx, leaderlock.ElectionOutboxPublisher, onElected)
	}()
	return done
}

func awaitRun(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		// A cancelled election is an ordinary shutdown, not a failure:
		// every service in this platform stops by cancelling a context.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want nil on shutdown", err)
		}
	case <-time.After(Timeout):
		t.Error("Run did not return after its context was cancelled")
	}
}

func holdUntilDone(announce func()) func(context.Context) {
	return func(leaderCtx context.Context) {
		announce()
		<-leaderCtx.Done()
	}
}

func closeOnce(ch chan struct{}) func() {
	closed := false
	return func() {
		if !closed {
			closed = true
			close(ch)
		}
	}
}
