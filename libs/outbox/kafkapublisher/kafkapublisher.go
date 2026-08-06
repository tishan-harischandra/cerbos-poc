// Package kafkapublisher is the real outbox.Publisher: it writes every
// outbox row's payload to a Kafka (or Redpanda) topic, keyed by the row's
// aggregate key so every event for one tenant/role lands on the same
// partition and is therefore delivered to consumers in the order it was
// written.
//
// Kafka is the invalidation transport, never the source of truth (§10.3): a
// publish failure here leaves the outbox row unpublished, for
// outbox.Loop's next drain to retry. Nothing about correctness depends on
// this package.
package kafkapublisher

import (
	"context"
	"fmt"

	"github.com/segmentio/kafka-go"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
)

// Writer is the slice of *kafka.Writer this package needs, narrowed so a
// test can supply a fake instead of a real broker connection.
type Writer interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
}

// Publisher implements outbox.Publisher over a Kafka topic.
type Publisher struct {
	Writer Writer
}

// New builds a Publisher writing to topic on the given brokers, using the
// least-surprising kafka-go defaults for this use case: a leastbytes-free
// balancer wrong for nothing here, since every message carries its own key.
func New(brokers []string, topic string) *Publisher {
	return &Publisher{Writer: &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Topic:                  topic,
		Balancer:               &kafka.Hash{},
		AllowAutoTopicCreation: true,
	}}
}

// Publish writes one outbox event's payload as a single Kafka message,
// keyed by the event's aggregate key.
func (p *Publisher) Publish(ctx context.Context, event assignmentstore.OutboxEvent) error {
	if err := p.Writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(event.AggregateKey),
		Value: []byte(event.Payload),
	}); err != nil {
		return fmt.Errorf("kafkapublisher: writing %s: %w", event.EventID, err)
	}
	return nil
}
