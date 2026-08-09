package lease_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/lease"
)

const (
	tick     = 5 * time.Millisecond
	patience = 2 * time.Second
)

func TestALeaseIsAcquiredAndTheCallerIsElected(t *testing.T) {
	holder := &fakeHolder{acquires: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	elected := make(chan struct{})
	done := run(ctx, holder, func(context.Context) { close(elected) })

	awaitClosed(t, elected, "the caller was never elected though the lease was free")
	cancel()
	awaitRun(t, done)
}

// A holder that cannot take the lease is a follower, not a failure: it keeps
// asking, because the current leader will eventually stop.
func TestAFollowerKeepsContendingUntilTheLeaseIsFree(t *testing.T) {
	holder := &fakeHolder{acquires: false}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	elected := make(chan struct{})
	done := run(ctx, holder, func(context.Context) { close(elected) })

	select {
	case <-elected:
		t.Fatal("a contender that never won the lease was elected anyway")
	case <-time.After(20 * tick):
	}
	if holder.acquireCalls() < 2 {
		t.Errorf("the follower asked %d times, want it to keep contending", holder.acquireCalls())
	}

	holder.setAcquires(true)
	awaitClosed(t, elected, "the follower was not elected once the lease came free")
	cancel()
	awaitRun(t, done)
}

// The lease has to be extended for as long as the caller is working, or the
// work is cut off by the clock rather than by anything going wrong.
func TestALeaderRenewsWhileItHoldsTheLease(t *testing.T) {
	holder := &fakeHolder{acquires: true, renews: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := run(ctx, holder, holdUntilCancelled)

	deadline := time.After(patience)
	for holder.renewCalls() < 3 {
		select {
		case <-deadline:
			t.Fatalf("the leader renewed %d times, want it to keep renewing", holder.renewCalls())
		case <-time.After(tick):
		}
	}
	cancel()
	awaitRun(t, done)
}

// The failure the whole port exists for: the lease is gone, so the work must
// stop, and the caller has to be able to tell that from an ordinary shutdown.
func TestLosingTheLeaseCancelsTheLeaderContextWithACause(t *testing.T) {
	holder := &fakeHolder{acquires: true, renews: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	leaderContexts := make(chan context.Context, 4)
	done := run(ctx, holder, func(leaderCtx context.Context) {
		leaderContexts <- leaderCtx
		<-leaderCtx.Done()
	})

	var leaderCtx context.Context
	select {
	case leaderCtx = <-leaderContexts:
	case <-time.After(patience):
		t.Fatal("the caller was never elected")
	}

	holder.setRenews(false)
	select {
	case <-leaderCtx.Done():
	case <-time.After(patience):
		t.Fatal("the leader context survived the loss of the lease")
	}
	if cause := context.Cause(leaderCtx); !errors.Is(cause, leaderlock.ErrLeadershipLost) {
		t.Errorf("cause = %v, want ErrLeadershipLost so a caller can tell this from a shutdown", cause)
	}

	// Losing an election is not fatal. The instance goes back to
	// contending, so a caller writes no retry loop of its own.
	holder.setAcquires(true)
	holder.setRenews(true)
	select {
	case <-leaderContexts:
	case <-time.After(patience):
		t.Fatal("a deposed leader never contended again")
	}

	cancel()
	awaitRun(t, done)
}

// A leader that shuts down politely must hand the election over immediately
// rather than making the fleet wait out its ttl.
func TestAShuttingDownLeaderReleasesTheLease(t *testing.T) {
	holder := &fakeHolder{acquires: true, renews: true}
	ctx, cancel := context.WithCancel(context.Background())

	elected := make(chan struct{})
	done := run(ctx, holder, func(leaderCtx context.Context) {
		close(elected)
		<-leaderCtx.Done()
	})
	awaitClosed(t, elected, "the caller was never elected")

	cancel()
	awaitRun(t, done)
	if holder.releaseCalls() != 1 {
		t.Errorf("Release was called %d times on shutdown, want exactly once", holder.releaseCalls())
	}
}

// The seam a contract test needs, and the failure a lease exists to survive:
// a leader stalls past its own lease - a paused process, a dropped network -
// and comes back to find the election has moved on. It must stay silent long
// enough for the lease to really expire, and it must stand down rather than
// carry on as though nothing happened.
func TestAStalledLeaderStopsRenewingAndThenStandsDown(t *testing.T) {
	holder := &fakeHolder{acquires: true, renews: true}
	stall := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	leaderContexts := make(chan context.Context, 4)
	done := runWith(ctx, lease.Config{
		TTL:           20 * tick,
		RenewInterval: tick,
		RetryInterval: tick,
		PauseRenewal:  stall,
	}, holder, func(leaderCtx context.Context) {
		leaderContexts <- leaderCtx
		<-leaderCtx.Done()
	})

	var leaderCtx context.Context
	select {
	case leaderCtx = <-leaderContexts:
	case <-time.After(patience):
		t.Fatal("the caller was never elected")
	}

	// While the leader is stalled the backend must hear nothing from it,
	// or its lease would be extended by the very outage being simulated.
	close(stall)
	time.Sleep(5 * tick)
	renewsWhenStalled := holder.renewCalls()
	time.Sleep(10 * tick)
	if holder.renewCalls() != renewsWhenStalled {
		t.Errorf("a stalled leader renewed %d more times, want it to stay silent while its lease expires",
			holder.renewCalls()-renewsWhenStalled)
	}

	// By the time it speaks again, a rival owns the lease.
	holder.setRenews(false)
	holder.setAcquires(false)
	select {
	case <-leaderCtx.Done():
	case <-time.After(patience):
		t.Fatal("a leader that stalled past its lease came back and kept leading")
	}
	if cause := context.Cause(leaderCtx); !errors.Is(cause, leaderlock.ErrLeadershipLost) {
		t.Errorf("cause = %v, want ErrLeadershipLost", cause)
	}
	if holder.releaseCalls() != 0 {
		t.Errorf("a deposed leader released the lease %d times, want it to leave a rival's lease alone", holder.releaseCalls())
	}

	cancel()
	awaitRun(t, done)
}

// A renewal that fails because the backend was unreachable is not the same
// answer as a rival holding the lease. The renew interval is a third of the
// ttl precisely so two renewals can fail before leadership is in danger; a
// leader that stood down on the first one would hand the election away on
// every brief database blip, and the interval would be promising a tolerance
// nothing delivered.
func TestALeaderRidesOutARenewalFailureShorterThanItsLease(t *testing.T) {
	holder := &fakeHolder{acquires: true, renews: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	leaderContexts := make(chan context.Context, 1)
	done := run(ctx, holder, func(leaderCtx context.Context) {
		leaderContexts <- leaderCtx
		<-leaderCtx.Done()
	})

	var leaderCtx context.Context
	select {
	case leaderCtx = <-leaderContexts:
	case <-time.After(patience):
		t.Fatal("the caller was never elected")
	}

	// The backend goes away for a few renewals, then comes back - well
	// inside the lease it already holds.
	holder.setRenewErr(errors.New("connection refused"))
	time.Sleep(4 * tick)
	select {
	case <-leaderCtx.Done():
		t.Fatalf("leadership ended on a transient renewal failure, %s into a %s lease: %v",
			4*tick, 20*tick, context.Cause(leaderCtx))
	default:
	}

	holder.setRenewErr(nil)
	time.Sleep(4 * tick)
	select {
	case <-leaderCtx.Done():
		t.Fatal("leadership ended after the backend came back and renewals succeeded again")
	default:
	}

	cancel()
	awaitRun(t, done)
}

// But it cannot ride it out forever. Once the backend has been unreachable for
// the whole lease, the lease has expired wherever it lives and a rival is
// entitled to it, so continuing to work would be the split-brain the ttl
// exists to bound.
func TestALeaderStandsDownOnceRenewalsHaveFailedForTheWholeLease(t *testing.T) {
	holder := &fakeHolder{acquires: true, renews: true}
	holder.setRenewErr(errors.New("connection refused"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	leaderContexts := make(chan context.Context, 4)
	done := run(ctx, holder, func(leaderCtx context.Context) {
		leaderContexts <- leaderCtx
		<-leaderCtx.Done()
	})

	var leaderCtx context.Context
	select {
	case leaderCtx = <-leaderContexts:
	case <-time.After(patience):
		t.Fatal("the caller was never elected")
	}

	select {
	case <-leaderCtx.Done():
	case <-time.After(patience):
		t.Fatal("a leader that could not renew for the whole lease kept working")
	}
	if cause := context.Cause(leaderCtx); !errors.Is(cause, leaderlock.ErrLeadershipLost) {
		t.Errorf("cause = %v, want ErrLeadershipLost", cause)
	}

	cancel()
	awaitRun(t, done)
}

// A rival holding the lease is a verdict, not an outage, so it takes effect at
// once rather than waiting out the lease.
func TestARivalTakingTheLeaseDeposesTheLeaderImmediately(t *testing.T) {
	holder := &fakeHolder{acquires: true, renews: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	leaderContexts := make(chan context.Context, 4)
	done := run(ctx, holder, func(leaderCtx context.Context) {
		leaderContexts <- leaderCtx
		<-leaderCtx.Done()
	})

	var leaderCtx context.Context
	select {
	case leaderCtx = <-leaderContexts:
	case <-time.After(patience):
		t.Fatal("the caller was never elected")
	}

	holder.setRenews(false)
	deadline := time.After(10 * tick)
	select {
	case <-leaderCtx.Done():
	case <-deadline:
		t.Fatalf("a deposed leader kept working for %s, want it to stand down at the next renewal", 10*tick)
	}

	cancel()
	awaitRun(t, done)
}

// A backend that is briefly unreachable is not a verdict about leadership.
// Reporting the error and asking again is what rides out a database failover;
// giving up would leave the election unheld until the process restarts.
func TestAnUnreachableBackendIsReportedAndRetried(t *testing.T) {
	unreachable := errors.New("connection refused")
	holder := &fakeHolder{acquireErr: unreachable}

	var mu sync.Mutex
	var reported []error
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := runWith(ctx, lease.Config{
		TTL:           20 * tick,
		RenewInterval: tick,
		RetryInterval: tick,
		OnError: func(err error) {
			mu.Lock()
			reported = append(reported, err)
			mu.Unlock()
		},
	}, holder, holdUntilCancelled)

	deadline := time.After(patience)
	for {
		mu.Lock()
		count := len(reported)
		mu.Unlock()
		if count >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("an unreachable backend was reported %d times, want the loop to keep trying", count)
		case <-time.After(tick):
		}
	}

	mu.Lock()
	first := reported[0]
	mu.Unlock()
	if !errors.Is(first, unreachable) {
		t.Errorf("reported %v, want it to wrap the backend's own error", first)
	}

	cancel()
	awaitRun(t, done)
}

func run(ctx context.Context, holder lease.Holder, onElected func(context.Context)) <-chan error {
	return runWith(ctx, lease.Config{
		TTL:           20 * tick,
		RenewInterval: tick,
		RetryInterval: tick,
	}, holder, onElected)
}

func runWith(ctx context.Context, cfg lease.Config, holder lease.Holder, onElected func(context.Context)) <-chan error {
	done := make(chan error, 1)
	go func() { done <- lease.Run(ctx, cfg, holder, onElected) }()
	return done
}

func holdUntilCancelled(leaderCtx context.Context) { <-leaderCtx.Done() }

func awaitClosed(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(patience):
		t.Fatal(message)
	}
}

func awaitRun(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want nil on shutdown", err)
		}
	case <-time.After(patience):
		t.Error("Run did not return after its context was cancelled")
	}
}

// fakeHolder is a lease backend whose answers a test can change while the
// loop is running, which is how leadership is taken away mid-flight.
type fakeHolder struct {
	mu         sync.Mutex
	acquires   bool
	renews     bool
	acquireErr error
	renewErr   error

	acquired int
	renewed  int
	released int
}

func (f *fakeHolder) Acquire(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquired++
	if f.acquireErr != nil {
		return false, f.acquireErr
	}
	return f.acquires, nil
}

func (f *fakeHolder) Renew(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewed++
	if f.renewErr != nil {
		return false, f.renewErr
	}
	return f.renews, nil
}

func (f *fakeHolder) Release(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released++
	return nil
}

func (f *fakeHolder) setAcquires(v bool)    { f.mu.Lock(); f.acquires = v; f.mu.Unlock() }
func (f *fakeHolder) setRenews(v bool)      { f.mu.Lock(); f.renews = v; f.mu.Unlock() }
func (f *fakeHolder) setRenewErr(err error) { f.mu.Lock(); f.renewErr = err; f.mu.Unlock() }

func (f *fakeHolder) acquireCalls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.acquired }
func (f *fakeHolder) renewCalls() int   { f.mu.Lock(); defer f.mu.Unlock(); return f.renewed }
func (f *fakeHolder) releaseCalls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.released }
