package notifications

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestOneSignalProviderSend(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		response     OneSignalResponse
		tokens       []DeviceTokenInfo
		wantStatuses []string
		wantError    bool
	}{
		{
			name:       "keeps invalid token positions in the result",
			statusCode: http.StatusOK,
			response:   OneSignalResponse{ID: "batch-id", Recipients: 2},
			tokens: []DeviceTokenInfo{
				{UserID: 1, PushToken: "short"},
				{UserID: 2, PushToken: "valid-token-123"},
				{UserID: 3, PushToken: "another-valid-token"},
			},
			wantStatuses: []string{"FAILED", "SENT", "SENT"},
		},
		{
			name:       "rejects non-success HTTP status",
			statusCode: http.StatusUnauthorized,
			response:   OneSignalResponse{Recipients: 0},
			tokens: []DeviceTokenInfo{
				{UserID: 1, PushToken: "valid-token-123"},
			},
			wantStatuses: []string{"FAILED"},
			wantError:    true,
		},
		{
			name:       "fails every forwarded token on partial acceptance",
			statusCode: http.StatusOK,
			response:   OneSignalResponse{ID: "batch-id", Recipients: 1},
			tokens: []DeviceTokenInfo{
				{UserID: 1, PushToken: "valid-token-123"},
				{UserID: 2, PushToken: "another-valid-token"},
			},
			wantStatuses: []string{"FAILED", "FAILED"},
			wantError:    true,
		},
		{
			name:       "returns per-token failures with batch error",
			statusCode: http.StatusServiceUnavailable,
			response:   OneSignalResponse{},
			tokens: []DeviceTokenInfo{
				{UserID: 1, PushToken: "valid-token-123"},
				{UserID: 2, PushToken: "another-valid-token"},
			},
			wantStatuses: []string{"FAILED", "FAILED"},
			wantError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := roundTripFunc(func(httpRequest *http.Request) (*http.Response, error) {
				if got := httpRequest.Header.Get("Authorization"); got != "Basic api-key" {
					t.Errorf("Authorization = %q, want %q", got, "Basic api-key")
				}
				var payload OneSignalRequest
				if err := json.NewDecoder(httpRequest.Body).Decode(&payload); err != nil {
					t.Errorf("decode request body: %v", err)
				}
				if got, want := len(payload.IncludePlayerIDs), validTokenCount(tt.tokens); got != want {
					t.Errorf("forwarded token count = %d, want %d", got, want)
				}
				body, err := json.Marshal(tt.response)
				if err != nil {
					return nil, err
				}
				return &http.Response{
					StatusCode: tt.statusCode,
					Body:       io.NopCloser(strings.NewReader(string(body))),
					Header:     make(http.Header),
					Request:    httpRequest,
				}, nil
			})

			provider := &OneSignalProvider{
				appID:    "app-id",
				apiKey:   "api-key",
				client:   &http.Client{Transport: transport},
				endpoint: "https://onesignal.test/notifications",
			}
			results, err := provider.Send(context.Background(), SendRequest{Tokens: tt.tokens})
			if (err != nil) != tt.wantError {
				t.Fatalf("Send error = %v, wantError %t", err, tt.wantError)
			}
			if len(results) != len(tt.tokens) {
				t.Fatalf("result count = %d, want %d", len(results), len(tt.tokens))
			}
			for i, want := range tt.wantStatuses {
				if results[i].Status != want {
					t.Errorf("result %d status = %q, want %q", i, results[i].Status, want)
				}
				if results[i].PushToken != tt.tokens[i].PushToken {
					t.Errorf("result %d token = %q, want %q", i, results[i].PushToken, tt.tokens[i].PushToken)
				}
				if want == "SENT" && results[i].ProviderMessageID != tt.response.ID {
					t.Errorf("result %d provider message ID = %q, want %q", i, results[i].ProviderMessageID, tt.response.ID)
				}
			}
		})
	}
}

func validTokenCount(tokens []DeviceTokenInfo) int {
	count := 0
	for _, token := range tokens {
		if token.PushToken != "" && len(token.PushToken) > minPushTokenLength {
			count++
		}
	}
	return count
}
