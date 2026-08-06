package kafkapublisher_test

import (
	"context"
	"errors"
	"testing"

	"github.com/segmentio/kafka-go"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/outbox/kafkapublisher"
)

type fakeWriter struct {
	written []kafka.Message
	err     error
}

func (w *fakeWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	if w.err != nil {
		return w.err
	}
	w.written = append(w.written, msgs...)
	return nil
}

func TestPublishWritesTheEventPayloadKeyedByItsAggregateKey(t *testing.T) {
	writer := &fakeWriter{}
	publisher := &kafkapublisher.Publisher{Writer: writer}

	err := publisher.Publish(context.Background(), assignmentstore.OutboxEvent{
		EventID:      "outbox-1",
		AggregateKey: "tenant-a:role-doctor",
		Payload:      `[{"tenantId":"tenant-a"}]`,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(writer.written) != 1 {
		t.Fatalf("wrote %d messages, want 1", len(writer.written))
	}
	if string(writer.written[0].Key) != "tenant-a:role-doctor" {
		t.Errorf("key = %q, want tenant-a:role-doctor", writer.written[0].Key)
	}
	if string(writer.written[0].Value) != `[{"tenantId":"tenant-a"}]` {
		t.Errorf("value = %q, want the event payload", writer.written[0].Value)
	}
}

func TestPublishWrapsAWriteFailure(t *testing.T) {
	writer := &fakeWriter{err: errors.New("broker unavailable")}
	publisher := &kafkapublisher.Publisher{Writer: writer}

	err := publisher.Publish(context.Background(), assignmentstore.OutboxEvent{EventID: "outbox-1"})
	if err == nil {
		t.Fatal("Publish returned nil despite the writer failing")
	}
}
