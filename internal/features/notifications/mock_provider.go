package notifications

import (
	"context"
	"fmt"
	"log"
)

type MockProvider struct{}

func NewMockProvider() *MockProvider {
	return &MockProvider{}
}

func (p *MockProvider) Send(ctx context.Context, req SendRequest) ([]SendResult, error) {
	log.Printf("[MOCK] Sending notification %d to %d devices", req.NotificationID, len(req.Tokens))
	results := make([]SendResult, len(req.Tokens))
	for i, token := range req.Tokens {
		results[i] = SendResult{
			UserID:            token.UserID,
			PushToken:         token.PushToken,
			ProviderMessageID: fmt.Sprintf("mock-%d-%d", req.NotificationID, token.UserID),
			Status:            "SENT",
			Error:             nil,
		}
		log.Printf("[MOCK]   -> User %d, token: %s...", token.UserID, token.PushToken[:8])
	}
	return results, nil
}
