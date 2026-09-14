// Package kafka defines the event contracts and the producer used to
// communicate between the API server, the scheduler, and the delivery
// worker over Kafka.
//
// # Topology
//
// Two topics participate in the notification pipeline:
//
//   - A send topic (KAFKA_SEND_TOPIC), carrying NotificationSendRequested
//     events produced by the API server (on immediate send) and the
//     scheduler (on scheduled promotion), and consumed by the delivery
//     worker's group.
//   - An update topic (KAFKA_UPDATE_TOPIC), carrying DeliveryUpdated events
//     produced by the worker after each provider call, and consumed by
//     downstream analytics (not part of this repository).
//
// # Wire format
//
// Every event is JSON-encoded. There is no schema registry and no version
// field: a change to a struct's fields is a breaking change for every
// consumer, and consumers are expected to tolerate unknown fields (the
// standard json.Unmarshal behaviour) so that additive changes are safe in
// the forward direction only. Removing or retyping a field is not safe
// without a coordinated consumer migration.
//
// # Partitioning
//
// The producer sets a message key so that related events route to the same
// partition, and therefore preserve order within a partition. Keys are
// built as "<prefix>_<id>" — see Producer.PublishSendRequested and
// Producer.PublishDeliveryUpdated for the prefixes and the caveats around
// the latter.
//
// # Concurrency
//
// The event structs are plain data and carry no internal synchronisation.
// Sharing an instance across goroutines requires the usual precautions; in
// practice each event is constructed and published from a single goroutine.
package kafka

import "time"

// NotificationSendRequested represents the event when a notification is requested to be sent.
//
// It is published by the API server when an operator triggers an immediate
// send, and by the scheduler when a scheduled notification's scheduled_at
// time arrives. The worker consumes it and begins the fan-out phase:
// resolving the notification's targets to a set of users, and each user to
// a set of device tokens.
//
// # Idempotency
//
// The event carries no unique identifier of its own. If the same
// NotificationID is observed twice — which can happen under Kafka's
// at-least-once delivery or if the scheduler's conditional update races —
// the worker will process it twice. The worker is responsible for
// deduplicating at the notification_deliveries layer (via a unique
// constraint or an existence check), not the producer. The IsRetry field
// is a producer hint for observability only; it does not participate in
// deduplication.
type NotificationSendRequested struct {
	// NotificationID is the internal notifications.id being dispatched.
	// It is the sole linkage between this event and the persisted campaign;
	// the worker re-reads the notification row from PostgreSQL rather than
	// trusting any other field on the event.
	NotificationID int64 `json:"notification_id"`

	// RequestedAt is the wall-clock time at which the event was produced.
	// It is distinct from ScheduledFor: RequestedAt is when the producer
	// decided to enqueue, while ScheduledFor records the intent to delay
	// dispatch until a future time.
	RequestedAt time.Time `json:"requested_at"`

	// IsRetry is an optional observability hint indicating that this event
	// is a re-publication of one that previously failed. It is omitted from
	// the JSON body when false. It has no functional effect on the worker;
	// deduplication is performed at the delivery layer regardless of this
	// flag.
	IsRetry bool `json:"is_retry,omitempty"`

	// ScheduledFor is an optional timestamp recording the notification's
	// originally intended dispatch time. When present, it allows consumers
	// to compute the end-to-end delay between the scheduled moment and the
	// actual dispatch. It is nil (and omitted from the JSON body) for
	// immediate sends.
	ScheduledFor *time.Time `json:"scheduled_for,omitempty"`
}

// DeliveryUpdated represents the event when a notification delivery status is updated.
//
// It is published by the worker after each per-device provider call
// completes — successfully or otherwise — and, in deployments that support
// it, when a client-reported event such as "opened" arrives. It is the
// asynchronous feed consumed by downstream analytics; the source of truth
// for the same status is the notification_deliveries row written by the
// worker in the same operation.
//
// # At-least-once semantics
//
// As with NotificationSendRequested, this event carries no event ID and no
// version. A downstream consumer that persists these events must be
// prepared to handle duplicates, either by keying on (NotificationID,
// UserID, Status, OccurredAt) or by tolerating re-application.
type DeliveryUpdated struct {
	// NotificationID is the internal notifications.id the delivery belongs
	// to.
	NotificationID int64 `json:"notification_id"`

	// UserID is the internal users.id the delivery was targeted at. It is
	// the user, not the device — a user with multiple active devices will
	// produce multiple DeliveryUpdated events with the same UserID and
	// NotificationID, distinguished by ProviderMessageID.
	UserID int64 `json:"user_id"`

	// Provider is the push provider that handled the delivery attempt — for
	// example, "onesignal" or "fcm". It allows downstream analytics to
	// attribute outcomes to a specific provider when more than one is in
	// use.
	Provider string `json:"provider"`

	// ProviderMessageID is the identifier assigned by the push provider to
	// the message. It is the stable key for correlating the event with
	// provider-side logs and receipts.
	ProviderMessageID string `json:"provider_message_id"`

	// Status is the new delivery status. The worker emits "SENT" after a
	// provider accepts a delivery attempt and "FAILED" when the provider
	// returns an error. Downstream consumers may later emit "DELIVERED" or
	// "OPENED" when provider receipts or client reports arrive; consumers
	// should tolerate forward-compatible values.
	Status string `json:"status"`

	// OccurredAt is the time at which the status change was observed by the
	// producer — that is, when the worker recorded the outcome, not
	// necessarily when the provider reported it.
	OccurredAt time.Time `json:"occurred_at"`

	// FailedReason is an optional human-readable description of the failure.
	// It is populated only when Status is "FAILED" and is omitted from the
	// JSON body otherwise. It is not a stable machine-readable code; the
	// provider's own error taxonomy lives in ProviderMessageID's
	// corresponding receipt.
	FailedReason string `json:"failed_reason,omitempty"`
}
