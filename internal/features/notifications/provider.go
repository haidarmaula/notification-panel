package notifications

import "context"

// NotificationProvider defines the interface for sending push notifications
// to device tokens. Implementations may use OneSignal, FCM, or other services.
type NotificationProvider interface {
	Send(ctx context.Context, req SendRequest) ([]SendResult, error)
}

// DeviceTokenInfo holds the essential data for a single device token.
type DeviceTokenInfo struct {
	UserID    int64
	PushToken string
	Platform  string
}

// SendRequest encapsulates notification content and target devices.
type SendRequest struct {
	NotificationID int64
	Title          string
	Body           string
	Tokens         []DeviceTokenInfo
	ClickAction    string
}

// SendResult represents the outcome for a single device.
type SendResult struct {
	UserID            int64
	PushToken         string
	ProviderMessageID string
	Status            string
	Error             error
}
