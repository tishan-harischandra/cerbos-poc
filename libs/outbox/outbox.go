// Package outbox drains the transactional outbox (§8.1) that
// assignmentstore.Store.SaveRoleMatrix appends to inside the same
// transaction as the permission write it accompanies.
//
// It never decides what a permission change means: this package's whole job
// is "take rows nobody has published yet and hand each to a Publisher,
// exactly once if the publish succeeds." What "publish" does - Kafka, once a
// later slice wires that in - is a caller's choice; this package holds no
// opinion about it.
package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// Store is the narrow slice of assignmentstore.Store the publisher loop
// needs: enough to drain the outbox, nothing that would let this package
// also write permissions.
type Store interface {
	UnpublishedOutboxEvents(ctx context.Context, limit int) ([]assignmentstore.OutboxEvent, error)
	MarkOutboxEventPublished(ctx context.Context, eventID string, publishedAt time.Time) error
}

// Publisher sends one outbox event onward. A row is marked published only
// after Publish returns nil, so a Publisher that fails leaves the row for
// the next drain to retry - delivery is at-least-once, and a downstream
// consumer must be idempotent on EventID.
type Publisher interface {
	Publish(ctx context.Context, event assignmentstore.OutboxEvent) error
}

// DefaultInterval is how often Loop.Run polls when LoopConfig.Interval is
// left zero.
const DefaultInterval = 2 * time.Second

// DefaultBatchSize bounds how many rows one poll drains when
// LoopConfig.BatchSize is left zero.
const DefaultBatchSize = 100

// LoopConfig holds a publisher loop's collaborators.
type LoopConfig struct {
	Store     Store
	Publisher Publisher
	// Interval is how often Run polls for unpublished events. Zero means
	// DefaultInterval.
	Interval time.Duration
	// BatchSize bounds how many rows one poll drains. Zero means
	// DefaultBatchSize.
	BatchSize int
	// Now is the clock a published row is stamped with. Injected so a test
	// can assert on a fixed value.
	Now func() time.Time
	// OnError, if set, is called with any error draining one event. A
	// drain error never stops the loop: an unpublished row simply stays
	// unpublished until the next poll tries again.
	OnError func(error)
}

// Loop polls Store for unpublished events and publishes each in turn.
type Loop struct {
	cfg LoopConfig
}

// NewLoop applies LoopConfig's defaults and returns a Loop.
func NewLoop(cfg LoopConfig) *Loop {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultBatchSize
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Loop{cfg: cfg}
}

// Run polls on Interval until ctx is done, draining one batch immediately
// before the first tick so a freshly started loop does not wait a whole
// interval to notice work that was already waiting.
func (l *Loop) Run(ctx context.Context) {
	l.DrainOnce(ctx)

	ticker := time.NewTicker(l.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.DrainOnce(ctx)
		}
	}
}

// DrainOnce publishes every currently unpublished event once, in the order
// Store reports them. It is exported so a caller, or a test, can drive one
// pass deterministically without waiting on Run's ticker.
func (l *Loop) DrainOnce(ctx context.Context) {
	events, err := l.cfg.Store.UnpublishedOutboxEvents(ctx, l.cfg.BatchSize)
	if err != nil {
		l.reportError(fmt.Errorf("outbox: reading unpublished events: %w", err))
		return
	}

	for _, event := range events {
		if err := l.cfg.Publisher.Publish(ctx, event); err != nil {
			l.reportError(fmt.Errorf("outbox: publishing %s: %w", event.EventID, err))
			continue
		}
		if err := l.cfg.Store.MarkOutboxEventPublished(ctx, event.EventID, l.cfg.Now()); err != nil {
			l.reportError(fmt.Errorf("outbox: marking %s published: %w", event.EventID, err))
		}
	}
}

func (l *Loop) reportError(err error) {
	if l.cfg.OnError != nil {
		l.cfg.OnError(err)
	}
}
