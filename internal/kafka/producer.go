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
// The underlying kafka.Writer is configured for at-most-one-leader
// acknowledgement (RequiredAcks: RequireOne) and for automatic topic
// creation (AllowAutoTopicCreation: true). Both choices favour availability
// and ease of local development over durability and strictness — see
// NewProducer for the trade-offs and the recommended changes for production
// deployments.
//
// # Delivery semantics
//
// A WriteMessages call returns nil once the broker has acknowledged the
// write under the configured RequiredAcks. This is not the same as
// "durable" — with RequireOne, a leader failure immediately after
// acknowledgment can lose the message. The producer does not retry at the
// application level; retries are handled internally by kafka-go within its
// own limits.
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
// # Configuration choices
//
// The writer is constructed with three non-default settings, each of which
// has an operational consequence:
//
//   - Balancer: kafka.LeastBytes. Message keys still determine partition
//     assignment when present (kafka-go hashes the key), so this balancer
//     only affects messages published without a key. In this codebase every
//     publish sets a key, so the balancer is effectively inert.
//   - RequiredAcks: kafka.RequireOne. The producer waits for the leader to
//     acknowledge, but not for replicas. A leader crash immediately after
//     acknowledgment can silently lose the message. Production deployments
//     that combine this producer with an at-least-once consumer should
//     consider kafka.RequireAll, at the cost of additional latency on every
//     publish.
//   - AllowAutoTopicCreation: true. A typo in the configured topic name
//     will silently create a new topic rather than failing loudly. This is
//     convenient during local development but hazardous in production,
//     where it can mask a misconfiguration: messages are written
//     successfully to a topic no consumer is reading. Deployments that
//     pre-provision topics should set this to false.
//
// # Parameters
//
// broker is the Kafka broker address in "host:port" form (or a
// comma-separated list, which kafka.TCP accepts and interprets as a seed
// list). topic is the target topic name. Neither is validated here; an
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
		RequiredAcks:           kafka.RequireOne,
		AllowAutoTopicCreation: true,
	}
	return &Producer{writer: w}
}

// PublishSendRequested publishes a send requested event.
//
// The event is JSON-marshalled and written to the producer's configured
// topic with a message key of the form "notification_<id>". Because the key
// is derived deterministically from the notification ID, all send-request
// events for the same notification route to the same partition, preserving
// their relative order.
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
// topic. The message key is derived from the notification ID and is
// intended to route all delivery updates for the same notification to the
// same partition, preserving their relative order for downstream analytics.
//
// # Message key
//
// The key is derived from the notification ID in the same way as
// PublishSendRequested, using strconv.FormatInt. All delivery-update events
// for a particular notification therefore share a deterministic key, and
// Kafka routes them to the same partition while preserving their relative
// order for downstream consumers.
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
