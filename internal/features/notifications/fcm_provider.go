package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"
)

// FCMProvider implements NotificationProvider using Firebase Cloud Messaging.
type FCMProvider struct {
	client *messaging.Client
}

// NewFCMProvider creates a new FCMProvider with the given service account credentials (JSON).
func NewFCMProvider(credentialsJSON []byte) (*FCMProvider, error) {
	// Parse credentials to extract project_id
	var creds map[string]interface{}
	if err := json.Unmarshal(credentialsJSON, &creds); err != nil {
		return nil, fmt.Errorf("invalid credentials JSON: %w", err)
	}

	projectID, ok := creds["project_id"].(string)
	if !ok || projectID == "" {
		return nil, fmt.Errorf("project_id not found in credentials")
	}

	opt := option.WithCredentialsJSON(credentialsJSON)
	config := &firebase.Config{
		ProjectID: projectID,
	}

	app, err := firebase.NewApp(context.Background(), config, opt)
	if err != nil {
		return nil, fmt.Errorf("firebase app init: %w", err)
	}

	client, err := app.Messaging(context.Background())
	if err != nil {
		return nil, fmt.Errorf("messaging client: %w", err)
	}

	return &FCMProvider{client: client}, nil
}

// Send sends notification via FCM.
func (p *FCMProvider) Send(ctx context.Context, req SendRequest) ([]SendResult, error) {
	log.Printf("[FCM] Sending notification %d to %d devices", req.NotificationID, len(req.Tokens))

	results := make([]SendResult, 0, len(req.Tokens))

	for _, tokenInfo := range req.Tokens {
		if tokenInfo.PushToken == "" || len(tokenInfo.PushToken) < 10 {
			result := SendResult{
				UserID:    tokenInfo.UserID,
				PushToken: tokenInfo.PushToken,
				Status:    "FAILED",
				Error:     fmt.Errorf("invalid push token format (empty or too short)"),
			}
			results = append(results, result)
			log.Printf("[FCM] Skipping invalid token for user %d", tokenInfo.UserID)
			continue
		}

		msg := &messaging.Message{
			Notification: &messaging.Notification{
				Title: req.Title,
				Body:  req.Body,
			},
			Token: tokenInfo.PushToken,
			Data: map[string]string{
				"notification_id": fmt.Sprintf("%d", req.NotificationID),
				"click_action":    "FLUTTER_NOTIFICATION_CLICK",
			},
			Android: &messaging.AndroidConfig{
				Priority: "high",
			},
			APNS: &messaging.APNSConfig{
				Headers: map[string]string{
					"apns-priority": "10",
				},
			},
		}

		response, err := p.client.Send(ctx, msg)
		result := SendResult{
			UserID:    tokenInfo.UserID,
			PushToken: tokenInfo.PushToken,
		}
		if err != nil {
			result.Status = "FAILED"
			result.Error = err
			log.Printf("[FCM] Failed for user %d: %v", tokenInfo.UserID, err)
		} else {
			result.Status = "SENT"
			result.ProviderMessageID = response
			log.Printf("[FCM] Sent to user %d, message ID: %s", tokenInfo.UserID, response)
		}
		results = append(results, result)
	}

	return results, nil
}
