package kafka

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/segmentio/kafka-go"
)

// Producer publishes events to a single Kafka topic.
//
// A Producer is bound to one topic at construction and cannot publish to
// another without being rebuilt. The API server, the scheduler, and the
// worker each construct their own Producer for the topic they need; the
// worker additionally holds a second Producer for the update topic.
//
// # Behaviour
//
// The underlying kafka.Writer is configured for all-replica acknowledgement
// (RequiredAcks: RequireAll) and for no automatic topic creation
// (AllowAutoTopicCreation: false). Both choices favour durability and
// strictness over convenience — see NewProducer for the trade-offs and the
// reasons these values were chosen.
//
// # Delivery semantics
//
// A WriteMessages call returns nil once the broker has acknowledged the
// write under the configured RequiredAcks. With RequireAll, the producer
// waits for the leader and all in-sync replicas to acknowledge the write,
// so a message that is acknowledged is durable across a single replica
// failure. The producer does not retry at the application level; retries
// are handled internally by kafka-go within its own limits.
//
// # Concurrency
//
// A Producer wraps a single kafka.Writer, which is safe for concurrent use
// by multiple goroutines. Multiple handlers may call PublishSendRequested
// or PublishDeliveryUpdated concurrently. Close should be called exactly
// once, after all publishing goroutines have stopped; calling Publish after
// Close is not supported.
type Producer struct {
	writer *kafka.Writer
}

// NewProducer creates a new Kafka producer with the given broker and topic.
// For development, you can pass "localhost:9092" and "notification.send.requested".
//
// This is the legacy producer. No cmd/ binary mounts it: its only caller is
// internal/features/notifications, which test files alone import. Deployed
// paths use NewScopedProducer or NewAuthenticatedScopedProducer, which hash
// message keys and take a validated broker slice.
//
// # Configuration choices
//
// The writer is constructed with three non-default settings, each of which
// has an operational consequence:
//
//   - Balancer: kafka.LeastBytes. This balancer does not hash the message key.
//     LeastBytes.Balance picks the partition with the fewest accumulated bytes
//     and counts len(msg.Key) only toward that total, so the deterministic
//     "notification_<id>" keys give neither partition affinity nor an ordering
//     guarantee. See finding H47; the scoped producer uses kafka.Hash instead.
//   - RequiredAcks: kafka.RequireAll. The producer waits for the leader and
//     all in-sync replicas to acknowledge before returning from
//     WriteMessages. This provides durability at the cost of additional
//     latency on every publish. It is the appropriate default for an
//     at-least-once delivery pipeline.
//   - AllowAutoTopicCreation: false. Topics must be pre-provisioned
//     explicitly. A typo in the configured topic name causes the first
//     publish to fail loudly rather than silently creating a topic no
//     consumer is reading. Pre-provisioning is the correct operational
//     posture for production; local development can create topics via the
//     provided Kafka UI or Kafka CLI.
//
// # Parameters
//
// broker is a single Kafka broker address in "host:port" form. kafka.TCP is
// variadic but never splits a comma-separated string, so a CSV value here is
// dialled as one literal hostname rather than used as a seed list; see finding
// M48. topic is the target topic name. Neither is validated here; an
// unreachable broker surfaces on the first publish attempt, not at
// construction. An empty topic name will cause the first publish to fail
// with a broker error.
//
// # Ownership
//
// The returned Producer owns its writer and must be closed by the caller
// via Close when it is no longer needed. Failure to close leaks a
// background goroutine and open connections to the broker. Callers should
// defer Close immediately after a successful construction.
func NewProducer(broker, topic string) *Producer {
	w := &kafka.Writer{
		Addr:                   kafka.TCP(broker),
		Topic:                  topic,
		Balancer:               &kafka.LeastBytes{},
		RequiredAcks:           kafka.RequireAll,
		AllowAutoTopicCreation: false,
	}
	return &Producer{writer: w}
}

// PublishSendRequested publishes a send requested event.
//
// The event is JSON-marshalled and written to the producer's configured
// topic with a message key of the form "notification_<id>". The key is
// deterministic, but the LeastBytes balancer ignores it when selecting a
// partition, so events for one notification carry no relative ordering
// guarantee on this legacy path. See finding H47.
//
// # Errors
//
// The method returns an error in two cases:
//
//   - JSON marshalling of the event fails. This is unexpected for a struct
//     of plain scalar and time fields, and indicates a programming error
//     rather than an environment problem.
//   - The underlying writer's WriteMessages returns an error — typically a
//     broker connection failure, an authentication failure, or a topic
//     authorisation problem.
//
// The event struct does not carry an event ID, so a failed publish cannot
// be de-duplicated by a retry on the consumer side. Callers that need
// retry-with-deduplication must add an event ID to the schema and coordinate
// the change with every consumer.
//
// # Concurrency
//
// Safe to call concurrently with other publishes on the same Producer.
func (p *Producer) PublishSendRequested(ctx context.Context, event NotificationSendRequested) error {
	value, err := json.Marshal(event)
	if err != nil {
		return err
	}

	key := []byte("notification_" + strconv.FormatInt(event.NotificationID, 10))
	msg := kafka.Message{
		Key:   key,
		Value: value,
	}

	return p.writer.WriteMessages(ctx, msg)
}

// PublishDeliveryUpdated publishes a delivery updated event.
//
// The event is JSON-marshalled and written to the producer's configured
// topic. The message key is derived from the notification ID.
//
// # Message key
//
// The key is derived from the notification ID in the same way as
// PublishSendRequested, using strconv.FormatInt, so every delivery-update
// event for a particular notification shares a deterministic key. The
// LeastBytes balancer ignores that key when selecting a partition, so the
// same-partition routing and relative ordering it would otherwise provide do
// not hold on this legacy path and downstream analytics must not rely on
// them. See finding H47.
//
// # Errors
//
// The method returns an error in the same two cases as
// PublishSendRequested: a JSON marshalling failure (unexpected) or a broker
// write failure. See that method for the deduplication caveat on retries.
//
// # Concurrency
//
// Safe to call concurrently with other publishes on the same Producer.
func (p *Producer) PublishDeliveryUpdated(ctx context.Context, event DeliveryUpdated) error {
	value, err := json.Marshal(event)
	if err != nil {
		return err
	}

	key := []byte("delivery_" + strconv.FormatInt(event.NotificationID, 10))
	msg := kafka.Message{
		Key:   key,
		Value: value,
	}

	return p.writer.WriteMessages(ctx, msg)
}

// Close closes the producer writer.
//
// It flushes any in-flight writes and releases the background goroutine and
// TCP connections owned by the underlying kafka.Writer. Close should be
// called exactly once, after all goroutines that might publish through the
// Producer have stopped; publishing after Close is not supported and will
// return a kafka-go internal error.
//
// The returned error is whatever kafka.Writer.Close reports, typically nil.
// Callers in this codebase use a bare defer p.Close() and discard the error,
// which is acceptable at process shutdown but should be logged if a
// long-running process ever needs to close and reopen its producers.
func (p *Producer) Close() error {
	return p.writer.Close()
}
