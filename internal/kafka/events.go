package kafka

import "time"

// NotificationSendRequested represents the event when a notification is requested to be sent.
type NotificationSendRequested struct {
	NotificationID int64      `json:"notification_id"`
	RequestedAt    time.Time  `json:"requested_at"`
	IsRetry        bool       `json:"is_retry,omitempty"`
	ScheduledFor   *time.Time `json:"scheduled_for,omitempty"`
}

// DeliveryUpdated represents the event when a notification delivery status is updated.
type DeliveryUpdated struct {
	NotificationID    int64     `json:"notification_id"`
	UserID            int64     `json:"user_id"`
	Provider          string    `json:"provider"`
	ProviderMessageID string    `json:"provider_message_id"`
	Status            string    `json:"status"` // DELIVERED, FAILED, OPENED
	OccurredAt        time.Time `json:"occurred_at"`
	FailedReason      string    `json:"failed_reason,omitempty"`
}
