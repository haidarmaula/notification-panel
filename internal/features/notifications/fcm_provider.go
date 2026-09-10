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
//
// It is an alternative to the default OneSignalProvider, shipping for
// deployments that prefer to dispatch through Firebase directly. Both
// providers implement the same NotificationProvider interface, so the
// selection is a startup-time configuration concern, not a code change.
//
// # Initialisation
//
// Unlike OneSignalProvider — which is constructed with an app ID and an API
// key — FCMProvider is constructed from a Google service-account JSON
// document. The constructor parses that document to extract the project ID
// and then hands the full document to the Firebase SDK for credential
// loading. The SDK holds its own connections to Firebase, which are not
// explicitly closed by this provider.
//
// # Concurrency
//
// The underlying messaging.Client is documented by the Firebase SDK as safe
// for concurrent use. FCMProvider therefore has no synchronisation of its
// own and is safe for concurrent Send calls.
type FCMProvider struct {
	client *messaging.Client
}

// NewFCMProvider creates a new FCMProvider with the given service account credentials (JSON).
//
// # Parameters
//
// credentialsJSON must be the raw bytes of a Google service-account key
// file. The constructor does not validate the JSON's schema beyond checking
// that a non-empty string "project_id" field is present; a syntactically
// valid JSON document with the wrong fields will pass that check and fail
// later, inside the Firebase SDK, with a less specific error. A missing or
// malformed project_id is the one input error the constructor detects
// itself, and it is reported with the message "project_id not found in
// credentials".
//
// # Context usage
//
// The constructor passes context.Background() to both firebase.NewApp and
// app.Messaging. Neither of these calls performs network I/O synchronously
// in the Firebase SDK, so the absence of a timeout here is not a startup
// hang risk — but any error the SDK returns will not be attributable to a
// caller-supplied deadline. If a future revision adds remote credential
// verification to this path, the context should become a parameter.
//
// # Errors
//
// Returns a non-nil error in four cases: the credentials document is not
// valid JSON, the document lacks a non-empty project_id, the Firebase app
// fails to initialise, or the messaging client fails to construct. In each
// case the returned error wraps the underlying cause with a short prefix
// ("invalid credentials JSON: ", "firebase app init: ", "messaging client:
// ") so that the failure mode is identifiable from logs without exposing
// the full SDK error.
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
//
// # Flow
//
// The method iterates over req.Tokens sequentially, dispatching one FCM
// message per token and collecting the result of each into the returned
// slice. It does not batch, pipeline, or parallelise the sends: each call
// to the Firebase SDK's Send method is a synchronous round-trip, and the
// method returns only after the last token has been attempted.
//
// For a fan-out of thousands of devices, this is a performance bottleneck —
// the Firebase SDK's SendAll method exists precisely to batch this pattern.
// The current implementation is appropriate for low-volume campaigns or
// for a fallback path, not for the primary delivery pipeline at scale.
//
// # Token validation
//
// Before dispatching, the method applies a length heuristic to each token:
// a token that is empty or shorter than 10 characters is treated as
// invalid and recorded as a "FAILED" result without contacting FCM. The
// threshold is a blunt instrument — it catches obviously-broken values
// while accepting anything of plausible length — and it is not derived
// from FCM's actual token format. A malformed token of sufficient length
// will therefore be sent to FCM and fail there, with the error reported
// in the per-token result.
//
// # Per-message configuration
//
// Each FCM message carries:
//
//   - The title and body from the SendRequest as an FCM Notification
//     object, which the OS renders when the app is backgrounded.
//   - A Data payload with "notification_id" (formatted from the numeric
//     request field) and a hardcoded "click_action" of
//     "FLUTTER_NOTIFICATION_CLICK". The latter is a client-side
//     integration detail: it assumes the mobile app is built with Flutter
//     and uses the flutter_local_notifications convention for
//     notification-tap handling. A non-Flutter client will not receive the
//     expected click_action.
//   - Android priority "high" and APNs header "apns-priority: 10", both of
//     which request immediate delivery rather than batched or deferred
//     handling. This is appropriate for user-visible notifications but
//     will be ignored by both platforms for messages that do not meet
//     their respective background-execution criteria.
//
// # Return values and error semantics
//
// The method returns a slice of SendResult, one per input token, in the
// same order as the input. Each result carries the token's user and push
// token, a Status of "SENT" or "FAILED", and — on failure — the underlying
// error. On success the result also carries the FCM-assigned message ID in
// ProviderMessageID.
//
// The second return value is always nil. Every failure, including a
// per-token SDK error, is captured in the results slice rather than
// returned as a function-level error. A caller who checks only the error
// return value will see a successful Send even when every token failed;
// callers must inspect the Status field of each SendResult to detect
// failures. This is a deliberate inversion of Go's usual error-handling
// convention and is a property of the NotificationProvider interface, not
// of this provider alone.
//
// # Concurrency
//
// Safe for concurrent use. The underlying Firebase messaging client is
// documented as concurrency-safe; the method allocates its own results
// slice and does not share state across invocations.
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
