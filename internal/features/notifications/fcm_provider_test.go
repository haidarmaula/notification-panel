package notifications

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"firebase.google.com/go/v4/messaging"
)

type fcmBatchSenderFunc func(context.Context, []*messaging.Message) (*messaging.BatchResponse, error)

func (f fcmBatchSenderFunc) SendAll(ctx context.Context, messages []*messaging.Message) (*messaging.BatchResponse, error) {
	return f(ctx, messages)
}

func TestFCMProviderSendReportsTotalFailure(t *testing.T) {
	provider := &FCMProvider{client: fcmBatchSenderFunc(func(context.Context, []*messaging.Message) (*messaging.BatchResponse, error) {
		return &messaging.BatchResponse{Responses: []*messaging.SendResponse{{Success: false, Error: errors.New("rejected")}}}, nil
	})}

	results, err := provider.Send(context.Background(), SendRequest{Tokens: []DeviceTokenInfo{{
		UserID: 1, PushToken: "valid-token-123",
	}}})
	if err == nil {
		t.Fatal("Send error = nil, want total-failure error")
	}
	if len(results) != 1 || results[0].Status != "FAILED" {
		t.Fatalf("results = %#v, want one FAILED result", results)
	}
}

func TestFCMProviderSendBatchesAndUsesCallerClickAction(t *testing.T) {
	const tokenCount = maxFCMMessagesPerBatch + 1
	tokens := make([]DeviceTokenInfo, tokenCount)
	for i := range tokens {
		tokens[i] = DeviceTokenInfo{UserID: int64(i + 1), PushToken: fmt.Sprintf("valid-token-%d", i)}
	}

	calls := 0
	provider := &FCMProvider{client: fcmBatchSenderFunc(func(_ context.Context, messages []*messaging.Message) (*messaging.BatchResponse, error) {
		calls++
		for i, message := range messages {
			if got := message.Data["click_action"]; got != "open_notification" {
				t.Errorf("message %d click_action = %q, want open_notification", i, got)
			}
		}
		responses := make([]*messaging.SendResponse, len(messages))
		for i := range responses {
			responses[i] = &messaging.SendResponse{Success: true, MessageID: fmt.Sprintf("message-%d", i)}
		}
		return &messaging.BatchResponse{Responses: responses}, nil
	})}

	results, err := provider.Send(context.Background(), SendRequest{
		ClickAction: "open_notification",
		Tokens:      tokens,
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("SendAll calls = %d, want 2 for %d tokens", calls, tokenCount)
	}
	if len(results) != tokenCount {
		t.Fatalf("result count = %d, want %d", len(results), tokenCount)
	}
	for i, result := range results {
		if result.Status != "SENT" {
			t.Errorf("result %d status = %q, want SENT", i, result.Status)
		}
	}
}

func TestFCMProviderSendOmitsEmptyClickAction(t *testing.T) {
	provider := &FCMProvider{client: fcmBatchSenderFunc(func(_ context.Context, messages []*messaging.Message) (*messaging.BatchResponse, error) {
		if _, ok := messages[0].Data["click_action"]; ok {
			t.Error("click_action is present for an empty request value")
		}
		return &messaging.BatchResponse{Responses: []*messaging.SendResponse{{Success: true, MessageID: "message-1"}}}, nil
	})}

	_, err := provider.Send(context.Background(), SendRequest{Tokens: []DeviceTokenInfo{{
		UserID: 1, PushToken: "valid-token-123",
	}}})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
}
