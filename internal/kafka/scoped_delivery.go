package kafka

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	broker "github.com/segmentio/kafka-go"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

// ErrScopedDeliveryEnvelope never contains event payload or provider details.
var ErrScopedDeliveryEnvelope = errors.New("invalid scoped delivery envelope")

// ScopedDeliveryUpdated is a complete version-2 projection. Stable event ID,
// version and occurred_at are persisted at mutation time, never at publication.
// ProviderAcceptedAt is server acceptance-evidence recording time, not delivery.
type ScopedDeliveryUpdated struct {
	SchemaVersion       int        `json:"schema_version"`
	ApplicationID       string     `json:"application_id"`
	OriginEpoch         string     `json:"origin_epoch"`
	SourceEpoch         string     `json:"source_epoch"`
	EventID             string     `json:"event_id"`
	DeliveryID          string     `json:"delivery_id"`
	DeliveryVersion     int64      `json:"delivery_version"`
	EventKind           string     `json:"event_kind"`
	NotificationID      int64      `json:"notification_id"`
	UserID              int64      `json:"user_id"`
	Provider            string     `json:"provider"`
	Status              string     `json:"status"`
	ProviderMessageID   *string    `json:"provider_message_id"`
	ProviderAcceptedAt  *time.Time `json:"provider_accepted_at"`
	ObservedDelivered   bool       `json:"observed_delivered"`
	ObservedDeliveredAt *time.Time `json:"observed_delivered_at"`
	ObservedOpened      bool       `json:"observed_opened"`
	ObservedOpenedAt    *time.Time `json:"observed_opened_at"`
	FailedAt            *time.Time `json:"failed_at"`
	Reason              *string    `json:"reason"`
	OccurredAt          time.Time  `json:"occurred_at"`
}

// Validate checks scope, truthful nullable evidence and bounded safe fields.
func (e ScopedDeliveryUpdated) Validate() error {
	if e.SchemaVersion != 2 || e.DeliveryVersion < 1 || e.NotificationID < 1 || e.UserID < 1 || e.Provider != "FCM" ||
		(e.EventKind != "submission" && e.EventKind != "observation") || !eventTime(e.OccurredAt) {
		return ErrScopedDeliveryEnvelope
	}
	for _, value := range []string{e.ApplicationID, e.OriginEpoch, e.SourceEpoch, e.EventID, e.DeliveryID} {
		var id pgtype.UUID
		if len(value) != 36 || id.Scan(value) != nil || id.Bytes == [16]byte{} || id.String() != value {
			return ErrScopedDeliveryEnvelope
		}
	}
	switch e.Status {
	case "PENDING", "SENT", "DELIVERED", "OPENED", "FAILED":
	default:
		return ErrScopedDeliveryEnvelope
	}
	if (e.ProviderMessageID == nil) != (e.ProviderAcceptedAt == nil) || (e.ObservedOpened && !e.ObservedDelivered) ||
		(e.Status == "OPENED" && !e.ObservedOpened) || (e.Status == "DELIVERED" && !e.ObservedDelivered) ||
		(e.ObservedDeliveredAt != nil && !e.ObservedDelivered) || (e.ObservedOpenedAt != nil && !e.ObservedOpened) {
		return ErrScopedDeliveryEnvelope
	}
	for _, value := range []*time.Time{e.ProviderAcceptedAt, e.ObservedDeliveredAt, e.ObservedOpenedAt, e.FailedAt} {
		if value != nil && !eventTime(*value) {
			return ErrScopedDeliveryEnvelope
		}
	}
	if e.ProviderMessageID != nil && (len(*e.ProviderMessageID) < 1 || len(*e.ProviderMessageID) > 1024 || strings.ContainsFunc(*e.ProviderMessageID, func(r rune) bool { return r > 127 || unicode.IsSpace(r) || unicode.IsControl(r) })) {
		return ErrScopedDeliveryEnvelope
	}
	if e.Reason != nil {
		if len(*e.Reason) < 1 || len(*e.Reason) > 64 {
			return ErrScopedDeliveryEnvelope
		}
		for _, b := range []byte(*e.Reason) {
			if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_') {
				return ErrScopedDeliveryEnvelope
			}
		}
	}
	return nil
}
func eventTime(t time.Time) bool {
	return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 && t.Nanosecond()%1000 == 0 && t.Location() == time.UTC
}

// DecodeScopedDelivery requires the complete closed v2 schema, preserving explicit
// null evidence while rejecting ambiguous JSON. The caller verifies its Kafka key
// and current approved source scope before applying a projection.
func DecodeScopedDelivery(value []byte) (ScopedDeliveryUpdated, error) {
	var event ScopedDeliveryUpdated
	if len(value) == 0 || len(value) > 65536 || !utf8.Valid(value) {
		return event, ErrScopedDeliveryEnvelope
	}
	nullable := map[string]bool{"provider_message_id": true, "provider_accepted_at": true, "observed_delivered_at": true, "observed_opened_at": true, "failed_at": true, "reason": true}
	fields := map[string]bool{}
	for _, name := range []string{"schema_version", "application_id", "origin_epoch", "source_epoch", "event_id", "delivery_id", "delivery_version", "event_kind", "notification_id", "user_id", "provider", "status", "provider_message_id", "provider_accepted_at", "observed_delivered", "observed_delivered_at", "observed_opened", "observed_opened_at", "failed_at", "reason", "occurred_at"} {
		fields[name] = false
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return event, ErrScopedDeliveryEnvelope
	}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		seen, known := fields[name]
		if err != nil || !ok || !known || seen {
			return event, ErrScopedDeliveryEnvelope
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && !nullable[name] {
			return event, ErrScopedDeliveryEnvelope
		}
		fields[name] = true
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return event, ErrScopedDeliveryEnvelope
	}
	for _, seen := range fields {
		if !seen {
			return event, ErrScopedDeliveryEnvelope
		}
	}
	var trailing json.RawMessage
	if decoder.Decode(&trailing) != io.EOF || json.Unmarshal(value, &event) != nil || event.Validate() != nil {
		return ScopedDeliveryUpdated{}, ErrScopedDeliveryEnvelope
	}
	return event, nil
}

// DeliveryUpdateKey preserves the established per-notification Kafka key format.
// Callers must validate the event before deriving its key.
func DeliveryUpdateKey(notificationID int64) []byte {
	return []byte("delivery_" + strconv.FormatInt(notificationID, 10))
}

// PublishScopedDelivery publishes an immutable copy of the exact persisted v2
// envelope. Cancellation can leave producer-owned I/O; Close must drain it.
func (p *ScopedProducer) PublishScopedDelivery(ctx context.Context, value []byte) error {
	event, err := DecodeScopedDelivery(value)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, broker.Message{Key: DeliveryUpdateKey(event.NotificationID), Value: bytes.Clone(value)})
}
