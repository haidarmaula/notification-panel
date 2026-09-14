package notifications

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDeliveryUpdatedEvent(t *testing.T) {
	occurredAt := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	failure := errors.New("provider rejected token")

	tests := []struct {
		name       string
		result     SendResult
		wantStatus string
		wantReason string
	}{
		{
			name: "accepted provider result",
			result: SendResult{
				UserID:            42,
				ProviderMessageID: "message-1",
				Status:            "SENT",
			},
			wantStatus: "SENT",
		},
		{
			name: "provider failure overrides result status",
			result: SendResult{
				UserID: 42,
				Status: "SENT",
				Error:  failure,
			},
			wantStatus: "FAILED",
			wantReason: failure.Error(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := deliveryUpdatedEvent(99, "ONESIGNAL", test.result, occurredAt)

			assert.Equal(t, int64(99), event.NotificationID)
			assert.Equal(t, int64(42), event.UserID)
			assert.Equal(t, "ONESIGNAL", event.Provider)
			assert.Equal(t, test.result.ProviderMessageID, event.ProviderMessageID)
			assert.Equal(t, test.wantStatus, event.Status)
			assert.Equal(t, occurredAt, event.OccurredAt)
			assert.Equal(t, test.wantReason, event.FailedReason)
		})
	}
}
