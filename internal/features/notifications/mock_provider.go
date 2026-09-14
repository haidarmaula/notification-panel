package notifications

import (
	"context"
	"fmt"
	"log"
)

// MockProvider is a NotificationProvider implementation that reports every
// send as successful without contacting any external service.
//
// It is intended for local development and automated testing: it allows the
// notification pipeline — the API's send trigger, the Kafka event, the
// worker's fan-out, and the delivery bookkeeping — to be exercised end to
// end without requiring OneSignal or FCM credentials. No network I/O is
// performed.
//
// # Determinism
//
// The ProviderMessageID assigned to each successful result is derived from
// the notification ID and the user ID: "mock-<notificationID>-<userID>".
// This makes the output stable across runs and allows tests to assert on
// the exact value. It also means that two sends of the same notification to
// the same user produce the same mock message ID, which is safe because the
// provider is stateless.
//
// # Selection
//
// The provider is chosen at process startup; this implementation is not
// wired into the production server or worker by default. Refer to the
// notification module's constructor to see how the active provider is
// selected.
//
// # Concurrency
//
// MockProvider holds no state and is safe for concurrent use.
type MockProvider struct{}

// NewMockProvider creates a new MockProvider.
//
// The constructor takes no arguments because the mock has no configuration.
// The returned value is stateless and can be shared freely between
// goroutines.
func NewMockProvider() *MockProvider {
	return &MockProvider{}
}

// Send reports every device in req as having received the notification
// successfully. No external call is made.
//
// # Behaviour
//
// The method iterates over req.Tokens, constructs a SendResult for each,
// and returns them in input order. Every result carries Status "SENT", a
// non-nil ProviderMessageID, and a nil Error field. The results slice is
// pre-allocated with the exact capacity of the input, so no intermediate
// allocations occur during the loop.
//
// # Short tokens
//
// Tokens shorter than 8 bytes are logged in full rather than truncated;
// the method never slices a token past its own length. Empty tokens are
// logged as an empty preview string. This makes the mock safe to drive
// with synthetic test fixtures that include empty or short push tokens.
//
// # Return values
//
// The error return is always nil. The NotificationProvider interface
// reserves it for whole-request failures — for example, a missing API key
// or a transport-level failure — which cannot occur in a mock. As with
// FCMProvider, callers must inspect the per-result Status rather than rely
// on the error return.
//
// # Context
//
// ctx is not used. The method performs no I/O and has no cancellable work.
// It accepts the parameter only to satisfy the NotificationProvider
// interface.
//
// # Concurrency
//
// Safe for concurrent use. The method allocates its own results slice and
// shares nothing across invocations.
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
		// Truncate the token for the log preview. Short tokens are logged
		// whole rather than sliced past their length; empty tokens render
		// as an empty preview.
		preview := token.PushToken
		if len(preview) > 8 {
			preview = preview[:8]
		}
		log.Printf("[MOCK]   -> User %d, token: %s...", token.UserID, preview)
	}
	return results, nil
}
