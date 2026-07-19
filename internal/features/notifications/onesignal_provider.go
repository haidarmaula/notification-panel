package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// OneSignalProvider implements NotificationProvider using OneSignal API.
type OneSignalProvider struct {
	appID  string
	apiKey string
	client *http.Client
}

// NewOneSignalProvider creates a new OneSignalProvider.
func NewOneSignalProvider(appID, apiKey string) *OneSignalProvider {
	return &OneSignalProvider{
		appID:  appID,
		apiKey: apiKey,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// OneSignalRequest represents the payload for OneSignal API.
type OneSignalRequest struct {
	AppID            string                 `json:"app_id"`
	IncludePlayerIDs []string               `json:"include_player_ids"`
	Headings         map[string]string      `json:"headings"`
	Contents         map[string]string      `json:"contents"`
	Data             map[string]interface{} `json:"data,omitempty"`
	AndroidChannelID string                 `json:"android_channel_id,omitempty"`
	Priority         int                    `json:"priority,omitempty"`
}

// OneSignalResponse represents the response from OneSignal API.
type OneSignalResponse struct {
	ID         string   `json:"id"`
	Recipients int      `json:"recipients"`
	Errors     []string `json:"errors"`
}

// Send sends notification via OneSignal API.
func (p *OneSignalProvider) Send(ctx context.Context, req SendRequest) ([]SendResult, error) {
	log.Printf("[OneSignal] Sending notification %d to %d devices", req.NotificationID, len(req.Tokens))

	if len(req.Tokens) == 0 {
		return []SendResult{}, nil
	}

	// Extract player IDs (push tokens) from tokens
	playerIDs := make([]string, 0, len(req.Tokens))
	userIDMap := make(map[string]int64) // playerID → userID

	for _, token := range req.Tokens {
		if token.PushToken != "" && len(token.PushToken) > 10 {
			playerIDs = append(playerIDs, token.PushToken)
			userIDMap[token.PushToken] = token.UserID
		}
	}

	if len(playerIDs) == 0 {
		log.Printf("[OneSignal] No valid player IDs for notification %d", req.NotificationID)
		return []SendResult{}, nil
	}

	// Build OneSignal request
	onesignalReq := OneSignalRequest{
		AppID:            p.appID,
		IncludePlayerIDs: playerIDs,
		Headings: map[string]string{
			"en": req.Title,
		},
		Contents: map[string]string{
			"en": req.Body,
		},
		Data: map[string]interface{}{
			"notification_id": req.NotificationID,
			"click_action":    "FLUTTER_NOTIFICATION_CLICK",
		},
		Priority: 10,
	}

	jsonBody, err := json.Marshal(onesignalReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Send to OneSignal API
	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://onesignal.com/api/v1/notifications", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Basic "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	var onesignalResp OneSignalResponse
	if err := json.NewDecoder(resp.Body).Decode(&onesignalResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(onesignalResp.Errors) > 0 {
		return nil, fmt.Errorf("onesignal error: %v", onesignalResp.Errors)
	}

	// Build results
	results := make([]SendResult, 0, len(playerIDs))
	for _, playerID := range playerIDs {
		results = append(results, SendResult{
			UserID:            userIDMap[playerID],
			PushToken:         playerID,
			ProviderMessageID: onesignalResp.ID,
			Status:            "SENT",
			Error:             nil,
		})
	}

	log.Printf("[OneSignal] Notification %d sent to %d devices, message ID: %s", req.NotificationID, len(results), onesignalResp.ID)
	return results, nil
}
