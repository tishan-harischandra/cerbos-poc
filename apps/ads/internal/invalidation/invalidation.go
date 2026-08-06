// Package invalidation is the consumer side of §10: turning a
// PermissionChanged message into exactly the cache invalidation it names,
// and measuring how long that took.
//
// Kafka is the transport, never the source of truth (§10.3): everything
// this package does is advisory. A message it never receives, or receives
// twice, or receives out of order, changes nothing about correctness - the
// reconciler in this same package repairs whatever this path misses, and
// PostgreSQL remains the only fact either of them ever writes.
package invalidation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/permissionevents"
)

// Cache is the narrow slice of assignments.CachingRoleMatrix this package
// needs to act on an invalidation.
type Cache interface {
	InvalidateRole(tenantID, roleID string)
	InvalidateRevision(tenantID string)
}

// Metrics is where a handled batch reports what happened. Both durations
// are non-negative for any event whose OccurredAt is not in the future;
// ObserveRevocationLatency is called only for an event whose Enabled is
// false (§10.3: "Revocation latency must be measured separately").
type Metrics interface {
	ObserveInvalidationLatency(d time.Duration)
	ObserveRevocationLatency(d time.Duration)
}

// Handler applies decoded PermissionChanged events to a Cache and reports
// timing to Metrics.
type Handler struct {
	Cache   Cache
	Metrics Metrics
	// Now is the clock latency is measured against. Injected so a test can
	// assert on a fixed value.
	Now func() time.Time
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// HandleMessage decodes one Kafka message value - a JSON array of
// PermissionChanged events, the outbox payload shape the admin-service
// writes (§10.2) - and applies every event in it.
//
// A malformed message is reported as an error rather than silently
// dropped: the reconciler will still converge the tenant it named, but a
// caller should have a chance to notice and alert on it.
func (h *Handler) HandleMessage(value []byte) error {
	var events []permissionevents.PermissionChanged
	if err := json.Unmarshal(value, &events); err != nil {
		return fmt.Errorf("invalidation: decoding a PermissionChanged batch: %w", err)
	}

	now := h.now()
	for _, event := range events {
		h.Cache.InvalidateRevision(event.TenantID)
		if event.SubjectType == permissionevents.SubjectRole {
			h.Cache.InvalidateRole(event.TenantID, event.SubjectID)
		}

		if h.Metrics == nil {
			continue
		}
		latency := now.Sub(event.OccurredAt)
		if latency < 0 {
			latency = 0
		}
		h.Metrics.ObserveInvalidationLatency(latency)
		if !event.Enabled {
			h.Metrics.ObserveRevocationLatency(latency)
		}
	}
	return nil
}

// Reader is the minimal slice of a Kafka consumer this package needs: read
// the next message's value, one at a time. Modelled narrowly so the
// production wiring (kafka-go) and a test fake satisfy it identically.
type Reader interface {
	ReadMessageValue(ctx context.Context) ([]byte, error)
}

// Consumer reads messages from a Reader and applies each with Handler,
// forever, until ctx is done. A handling error is reported through OnError
// and the loop continues: one malformed message must not stop every
// tenant's invalidation from ever being processed again.
type Consumer struct {
	Reader  Reader
	Handler *Handler
	OnError func(error)
}

// Run reads and handles messages until ctx is done.
func (c *Consumer) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		value, err := c.Reader.ReadMessageValue(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.reportError(fmt.Errorf("invalidation: reading a message: %w", err))
			continue
		}
		if err := c.Handler.HandleMessage(value); err != nil {
			c.reportError(err)
		}
	}
}

func (c *Consumer) reportError(err error) {
	if c.OnError != nil {
		c.OnError(err)
	}
}
