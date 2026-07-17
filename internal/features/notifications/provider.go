package notifications

import "context"

type NotificationProvider interface {
	Send(ctx context.Context, req SendRequest) ([]SendResult, error)
}

type SendRequest struct {
	NotificationID int64
	Title          string
	Body           string
	Tokens         []DeviceTokenInfo
}

type DeviceTokenInfo struct {
	UserID    int64
	PushToken string
	Platform  string
}

type SendResult struct {
	UserID            int64
	PushToken         string
	ProviderMessageID string
	Status            string
	Error             error
}
