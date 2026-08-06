package invalidation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/segmentio/kafka-go"
	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/invalidation"
)

type fakeMessageReader struct {
	messages []kafka.Message
	i        int
	err      error
}

func (r *fakeMessageReader) ReadMessage(context.Context) (kafka.Message, error) {
	if r.err != nil {
		return kafka.Message{}, r.err
	}
	if r.i >= len(r.messages) {
		return kafka.Message{}, errors.New("no more messages")
	}
	msg := r.messages[r.i]
	r.i++
	return msg, nil
}

func TestKafkaReaderReadMessageValueReturnsTheMessageValue(t *testing.T) {
	reader := &invalidation.KafkaReader{Reader: &fakeMessageReader{
		messages: []kafka.Message{{Value: []byte(`[{"tenantId":"tenant-a"}]`)}},
	}}

	value, err := reader.ReadMessageValue(context.Background())
	if err != nil {
		t.Fatalf("ReadMessageValue: %v", err)
	}
	if string(value) != `[{"tenantId":"tenant-a"}]` {
		t.Errorf("value = %q, want the message value", value)
	}
}

func TestKafkaReaderReadMessageValuePropagatesAReadError(t *testing.T) {
	reader := &invalidation.KafkaReader{Reader: &fakeMessageReader{err: errors.New("broker unavailable")}}

	if _, err := reader.ReadMessageValue(context.Background()); err == nil {
		t.Fatal("ReadMessageValue returned nil despite the reader failing")
	}
}
