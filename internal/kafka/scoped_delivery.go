package kafka

import (
	"errors"
	"strings"
	"time"
	"unicode"

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
