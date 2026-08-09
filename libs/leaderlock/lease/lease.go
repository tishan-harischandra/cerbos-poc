// Package lease is the campaign loop the lease-based leader election
// adapters share.
//
// Every lease backend answers the same three questions - can I take it, can I
// keep it, here it is back - and differs only in how it asks them. The part
// that is easy to get wrong is not the asking: it is the ordering around it,
// where a renewal that fails has to cancel the caller's work before a rival
// starts, and a shutdown has to hand the lease back rather than leave the
// fleet waiting out a ttl. Writing that once means an adapter is three
// queries, and a bug fixed here is fixed for all of them.
//
// This package is not an adapter. It names no backend and is safe for the
// adapters to share; consumers still depend only on leaderlock.Elector.
package lease

import (
	"context"
	"fmt"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
)

// Holder is one backend's lease primitives.
//
// Every method is expected to be quick and to report a backend that is merely
// unreachable as an error rather than as a verdict: "I could not ask" is not
// the same answer as "somebody else holds it", and conflating them hands the
// election away during a failover.
type Holder interface {
	// Acquire takes the lease if it is free or expired, and reports
	// whether this instance now holds it.
	Acquire(ctx context.Context) (bool, error)
	// Renew extends a lease this instance already holds. It reports false
	// when the lease is no longer ours - the case a rival has taken over.
	Renew(ctx context.Context) (bool, error)
	// Release gives the lease up. It is called on a clean shutdown so the
	// election turns over at once instead of after a ttl.
	Release(ctx context.Context) error
}

// Config tunes one campaign.
type Config struct {
	// TTL is how long the lease survives unrenewed. The loop does not
	// enforce it - the backend does - but it bounds how long a renewal
	// may take before the lease is presumed lost.
	TTL time.Duration
	// RenewInterval is how often a leader renews.
	RenewInterval time.Duration
	// RetryInterval is how often a follower re-contends.
	RetryInterval time.Duration

	// PauseRenewal, once it fires, stalls this instance: it stops renewing
	// for longer than the lease lives, and then resumes.
	//
	// That is a leader whose process was paused, or whose network dropped,
	// and which then came back - the failure a lease exists to survive. It
	// is modelled as a stall rather than a death on purpose: a leader that
	// never wakes up cannot observe anything, so there would be nothing to
	// assert about it, while a leader that wakes up must discover the
	// election has moved on and stand down. It exists for the contract
	// suite; production leaves it nil.
	PauseRenewal <-chan struct{}

	// OnError, if set, receives every backend failure. A failure never
	// ends the campaign.
	OnError func(error)
}

// DefaultRetryInterval is used when Config.RetryInterval is not set.
const DefaultRetryInterval = 2 * time.Second

// Run campaigns for the lease until ctx is done.
func Run(ctx context.Context, cfg Config, holder Holder, onElected func(leaderCtx context.Context)) error {
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = DefaultRetryInterval
	}
	if cfg.RenewInterval <= 0 {
		cfg.RenewInterval = cfg.TTL / 3
	}
	if cfg.RenewInterval <= 0 {
		return fmt.Errorf("lease: a renew interval or a ttl is required")
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		acquired, err := holder.Acquire(ctx)
		if err != nil {
			// Unreachable is not a verdict. Report it and ask again:
			// giving up here would leave the election unheld until
			// somebody restarts the process.
			report(cfg, fmt.Errorf("lease: contending for leadership: %w", err))
		}
		if !acquired {
			if !sleep(ctx, cfg.RetryInterval) {
				return nil
			}
			continue
		}

		hold(ctx, cfg, holder, onElected)
	}
}

// hold runs the caller's work under a leadership context and keeps the lease
// alive underneath it, returning when leadership ends for any reason.
func hold(ctx context.Context, cfg Config, holder Holder, onElected func(context.Context)) {
	leaderCtx, endLeadership := context.WithCancelCause(ctx)

	// The caller's work runs in its own goroutine so renewal is never
	// blocked by it. A caller that ignores its leaderCtx would otherwise
	// stop the very renewals that keep it leader.
	working := make(chan struct{})
	go func() {
		defer close(working)
		onElected(leaderCtx)
	}()

	renewals := time.NewTicker(cfg.RenewInterval)
	defer renewals.Stop()

	// While a stall is in progress no renewal is sent, so the lease really
	// does expire in the backend and a rival really does take it.
	var stallUntil <-chan time.Time
	pause := cfg.PauseRenewal

	// When this instance last knew the lease was its own. A renewal that
	// fails because the backend was unreachable is not an answer about
	// leadership, so it is measured against this rather than acted on:
	// the lease is still held until it actually expires.
	heldSince := time.Now()
	for {
		select {
		case <-ctx.Done():
			endLeadership(ctx.Err())
			<-working
			// The shutdown context is already cancelled, so releasing
			// needs a fresh one or the release never leaves the process
			// and the fleet waits out the whole ttl for nothing.
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout(cfg))
			if err := holder.Release(releaseCtx); err != nil {
				report(cfg, fmt.Errorf("lease: releasing leadership: %w", err))
			}
			cancel()
			return

		case <-pause:
			// Stop touching the backend for longer than the lease
			// lives. Clearing the channel keeps a closed one from
			// re-firing on every pass through the select.
			pause = nil
			stallUntil = time.After(stallFor(cfg))

		case <-stallUntil:
			stallUntil = nil

		case <-renewals.C:
			if stallUntil != nil {
				continue
			}
			renewCtx, cancel := context.WithTimeout(ctx, cfg.RenewInterval)
			held, err := holder.Renew(renewCtx)
			cancel()

			if err != nil {
				report(cfg, fmt.Errorf("lease: renewing leadership: %w", err))
				// "I could not ask" is not "somebody else holds
				// it". The renew interval is a fraction of the
				// ttl so that a couple of attempts can fail
				// without costing the election; standing down
				// on the first would hand the election away on
				// every brief blip. But only until the lease
				// would really have expired - past that, a
				// rival is entitled to it and carrying on would
				// be the split-brain the ttl exists to bound.
				if cfg.TTL <= 0 || time.Since(heldSince) < cfg.TTL {
					continue
				}
			} else if held {
				heldSince = time.Now()
				continue
			}

			// The lease is gone, either because a rival answered
			// for it or because it expired while the backend was
			// unreachable. The caller's work must stop before a
			// rival's starts, and it has to be able to tell this
			// from an ordinary shutdown.
			endLeadership(leaderlock.ErrLeadershipLost)
			<-working
			return
		}
	}
}

// stallFor is how long a stalled leader stays silent: past the whole lease,
// plus a renewal interval, so the lease has certainly expired by the time it
// speaks again.
func stallFor(cfg Config) time.Duration {
	stall := cfg.TTL + cfg.RenewInterval
	if cfg.TTL <= 0 {
		stall = 4 * cfg.RenewInterval
	}
	return stall
}

// releaseTimeout bounds the handover on shutdown. It is short: a service is
// already stopping, and a lease that cannot be handed back simply expires.
func releaseTimeout(cfg Config) time.Duration {
	if cfg.RenewInterval > 0 && cfg.RenewInterval < 5*time.Second {
		return 5 * time.Second
	}
	return cfg.RenewInterval
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func report(cfg Config, err error) {
	if cfg.OnError != nil {
		cfg.OnError(err)
	}
}
