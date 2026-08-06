package outbox_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/outbox"
)

// fakeStore is an in-memory Store: enough to drive DrainOnce deterministically
// without a database.
type fakeStore struct {
	mu          sync.Mutex
	unpublished []assignmentstore.OutboxEvent
	published   map[string]time.Time
	readErr     error
	markErr     error
}

func newFakeStore(events ...assignmentstore.OutboxEvent) *fakeStore {
	return &fakeStore{unpublished: events, published: map[string]time.Time{}}
}

func (f *fakeStore) UnpublishedOutboxEvents(_ context.Context, limit int) ([]assignmentstore.OutboxEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return nil, f.readErr
	}
	var out []assignmentstore.OutboxEvent
	for _, event := range f.unpublished {
		if _, done := f.published[event.EventID]; done {
			continue
		}
		out = append(out, event)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) MarkOutboxEventPublished(_ context.Context, eventID string, publishedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
	f.published[eventID] = publishedAt
	return nil
}

func (f *fakeStore) isPublished(eventID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.published[eventID]
	return ok
}

// fakePublisher records what it was asked to publish and can be told to
// fail for a named event, so a test can force a partial-batch failure.
type fakePublisher struct {
	mu        sync.Mutex
	published []string
	failFor   map[string]error
}

func newFakePublisher() *fakePublisher {
	return &fakePublisher{failFor: map[string]error{}}
}

func (p *fakePublisher) Publish(_ context.Context, event assignmentstore.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err, fail := p.failFor[event.EventID]; fail {
		return err
	}
	p.published = append(p.published, event.EventID)
	return nil
}

func (p *fakePublisher) publishedIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.published...)
}

func TestDrainOncePublishesAndMarksEveryUnpublishedEvent(t *testing.T) {
	store := newFakeStore(
		assignmentstore.OutboxEvent{EventID: "outbox-1", EventType: "permission.changed"},
		assignmentstore.OutboxEvent{EventID: "outbox-2", EventType: "permission.changed"},
	)
	publisher := newFakePublisher()
	loop := outbox.NewLoop(outbox.LoopConfig{Store: store, Publisher: publisher})

	loop.DrainOnce(context.Background())

	got := publisher.publishedIDs()
	if len(got) != 2 || got[0] != "outbox-1" || got[1] != "outbox-2" {
		t.Fatalf("published %v, want [outbox-1 outbox-2] in order", got)
	}
	if !store.isPublished("outbox-1") || !store.isPublished("outbox-2") {
		t.Error("both events should be marked published")
	}
}

// A publish failure must not stop the batch, and must leave that one event
// unpublished for the next drain to retry (at-least-once delivery).
func TestAPublishFailureLeavesThatEventUnpublishedButDrainsTheRest(t *testing.T) {
	store := newFakeStore(
		assignmentstore.OutboxEvent{EventID: "outbox-1", EventType: "permission.changed"},
		assignmentstore.OutboxEvent{EventID: "outbox-2", EventType: "permission.changed"},
		assignmentstore.OutboxEvent{EventID: "outbox-3", EventType: "permission.changed"},
	)
	publisher := newFakePublisher()
	publisher.failFor["outbox-2"] = errors.New("boom")

	var reported []error
	loop := outbox.NewLoop(outbox.LoopConfig{
		Store: store, Publisher: publisher,
		OnError: func(err error) { reported = append(reported, err) },
	})

	loop.DrainOnce(context.Background())

	if store.isPublished("outbox-2") {
		t.Error("outbox-2 was marked published despite Publish failing")
	}
	if !store.isPublished("outbox-1") || !store.isPublished("outbox-3") {
		t.Error("outbox-1 and outbox-3 should still be published despite outbox-2 failing")
	}
	if len(reported) != 1 {
		t.Fatalf("OnError was called %d times, want 1", len(reported))
	}
}

// A second drain must not re-publish rows the first drain already marked
// published: the fake store's unpublished list, like the real one, only
// ever reports rows with no publication time.
func TestASecondDrainDoesNotRepublishAlreadyPublishedEvents(t *testing.T) {
	store := newFakeStore(assignmentstore.OutboxEvent{EventID: "outbox-1"})
	publisher := newFakePublisher()
	loop := outbox.NewLoop(outbox.LoopConfig{Store: store, Publisher: publisher})

	loop.DrainOnce(context.Background())
	loop.DrainOnce(context.Background())

	if got := publisher.publishedIDs(); len(got) != 1 {
		t.Fatalf("published %v after two drains, want exactly one publish", got)
	}
}

func TestARunFailureOnReadIsReportedAndDoesNotPanic(t *testing.T) {
	store := newFakeStore()
	store.readErr = errors.New("connection reset")
	publisher := newFakePublisher()

	var reported error
	loop := outbox.NewLoop(outbox.LoopConfig{
		Store: store, Publisher: publisher,
		OnError: func(err error) { reported = err },
	})

	loop.DrainOnce(context.Background())

	if reported == nil {
		t.Fatal("a read failure should be reported through OnError")
	}
}

func TestRunDrainsImmediatelyThenStopsWhenContextIsCancelled(t *testing.T) {
	store := newFakeStore(assignmentstore.OutboxEvent{EventID: "outbox-1"})
	publisher := newFakePublisher()
	loop := outbox.NewLoop(outbox.LoopConfig{
		Store: store, Publisher: publisher, Interval: time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		loop.Run(ctx)
		close(done)
	}()

	// Run must drain once before ever waiting on the (hour-long) ticker.
	deadline := time.After(time.Second)
	for {
		if len(publisher.publishedIDs()) == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run did not drain immediately on start")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
