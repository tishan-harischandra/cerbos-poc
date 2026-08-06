package invalidation

import (
	"context"

	"github.com/segmentio/kafka-go"
)

// MessageReader is the slice of *kafka.Reader this package needs, narrowed
// so a test can supply a fake instead of a real broker connection.
type MessageReader interface {
	ReadMessage(ctx context.Context) (kafka.Message, error)
}

// KafkaReader adapts a MessageReader to the Reader interface Consumer
// drives.
type KafkaReader struct {
	Reader MessageReader
}

// NewKafkaReader builds a KafkaReader consuming topic on the given
// brokers, in consumer group groupID. The group is what lets several ADS
// replicas share the topic's partitions rather than each reading every
// message: consumer lag and partition assignment are then properties of the
// group, exported by the same *kafka.Reader this wraps.
func NewKafkaReader(brokers []string, topic, groupID string) *KafkaReader {
	return &KafkaReader{Reader: kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: groupID,
	})}
}

// ReadMessageValue reads the next message's value.
func (r *KafkaReader) ReadMessageValue(ctx context.Context) ([]byte, error) {
	msg, err := r.Reader.ReadMessage(ctx)
	if err != nil {
		return nil, err
	}
	return msg.Value, nil
}

// Lag reports the underlying *kafka.Reader's current consumer group lag,
// summed across the partitions this replica is assigned. It reports 0 for
// any Reader that does not expose stats - a test fake, most likely - rather
// than panicking, since lag is an observability signal, not a correctness
// dependency.
func (r *KafkaReader) Lag() int64 {
	stats, ok := r.Reader.(interface{ Stats() kafka.ReaderStats })
	if !ok {
		return 0
	}
	return stats.Stats().Lag
}
