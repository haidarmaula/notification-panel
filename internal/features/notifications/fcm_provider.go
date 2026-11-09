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

// maxFCMMessagesPerBatch is the maximum number of messages the Firebase
// Admin SDK accepts in a single SendAll call.
//
// The SDK rejects a call that exceeds this limit with an "at most 500
// messages can be sent at a time" error. The provider therefore chunks its
// outgoing work into groups of this size before calling SendAll, so that a
// large fan-out produces multiple sequential HTTP round-trips rather than
// a single rejected one.
//
// The value is a property of the Firebase SDK, not of this provider. If a
// future SDK revision raises the limit, this constant can be raised in
// lockstep without any other change.
const maxFCMMessagesPerBatch = 500

type fcmBatchSender interface {
	SendAll(context.Context, []*messaging.Message) (*messaging.BatchResponse, error)
}

// FCMProvider implements NotificationProvider using Firebase Cloud Messaging.
//
// This adapter belongs to the legacy notifications feature and is not
// constructed by the current worker binary. The protected worker uses the
// route-bound FCM transport in internal/dispatch.
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
// # Error contract
//
// Send populates one SendResult per input token regardless of outcome —
// including on non-nil-error returns — and additionally returns a non-nil
// function-level error when every token failed. This closes finding H24 in
// code-review.md: a previous revision returned a nil error on every path,
// so a caller that checked only the error return could not distinguish a
// fully-successful fan-out from a catastrophic one.
//
// The contract is deliberately asymmetric: per-token partial failures do
// NOT produce a function-level error, because the caller is expected to
// inspect the Status field of each result for per-token detail. Only the
// "nothing succeeded" case is escalated to the function-level return.
// Callers must inspect both the error return and the result slice to
// recover the full outcome.
//
// # Concurrency
//
// The underlying messaging.Client is documented by the Firebase SDK as safe
// for concurrent use. FCMProvider therefore has no synchronisation of its
// own and is safe for concurrent Send calls.
type FCMProvider struct {
	client fcmBatchSender
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

// Send sends the notification to every token in req via Firebase Cloud
// Messaging, batching the outgoing requests through the SDK's SendAll
// method.
//
// # Contract
//
// On any return — nil error or non-nil error — the result slice has exactly
// one SendResult per entry in req.Tokens, in the same order as the input. A
// caller can therefore correlate result i with req.Tokens[i] by index.
//
// Each result carries a Status of "SENT" or "FAILED":
//
//   - "SENT" means the Firebase SDK accepted the message for delivery; the
//     result also carries the FCM-assigned message ID in
//     ProviderMessageID. Because each message is sent with its own token,
//     the message IDs are distinct per recipient — unlike OneSignalProvider,
//     which returns the same ID for every device in the batch.
//   - "FAILED" means the token was not delivered. The result's Error field
//     carries the underlying cause, which may be a local validation
//     rejection, a per-token rejection by FCM, or a batch-level transport
//     failure that affected the message's chunk.
//
// # Function-level error
//
// Send returns a non-nil function-level error if and only if no token
// succeeded — either because every token failed local validation, or
// because every dispatched message failed at FCM. Partial failures return
// a nil error; callers that need to detect them must iterate the result
// slice and inspect each Status.
//
// This behaviour closes finding H24 in code-review.md. A previous revision
// always returned a nil error, so a caller that checked only the error
// return saw an "all-successful" signal even when every token had failed.
//
// # Batching
//
// The method groups its outgoing messages into chunks of at most
// maxFCMMessagesPerBatch and dispatches each chunk with a single call to
// the Firebase SDK's SendAll. This closes finding H25 in code-review.md:
// a previous revision called the SDK's per-message Send method in a
// sequential loop, paying a full HTTP round-trip per token and ignoring
// the batching primitive that the SDK provides.
//
// A fan-out of N tokens is therefore dispatched in ceil(N/500) HTTP calls
// rather than N, which for a campaign of thousands of devices is the
// difference between seconds and minutes. The chunks are dispatched
// sequentially — the method does not run them in parallel — so a caller
// that needs bounded concurrency across chunks must orchestrate it
// itself.
//
// # Token validation
//
// Before dispatch, the method applies a length heuristic to each token: a
// token that is empty or whose byte length is at most minPushTokenLength
// is treated as invalid and recorded as a "FAILED" result without being
// sent to FCM. The threshold matches the one used by OneSignalProvider so
// that both providers accept and reject the same token population. A
// malformed token of sufficient length will still be sent to FCM and fail
// there, with the per-token FCM error reported in the result.
//
// # Per-message configuration
//
// Each FCM message carries:
//
//   - The title and body from the SendRequest as an FCM Notification
//     object, which the OS renders when the app is backgrounded.
//   - A Data payload with "notification_id" (formatted from the numeric
//     request field) and, when req.ClickAction is non-empty,
//     "click_action" with its value. The key is omitted entirely when
//     req.ClickAction is empty, so no client-side integration detail is
//     embedded in the server. This closes finding H26 in code-review.md:
//     a previous revision hardcoded "FLUTTER_NOTIFICATION_CLICK", which
//     assumed a Flutter client using the flutter_local_notifications
//     convention and would silently mis-configure any other client.
//   - Android priority "high" and APNs header "apns-priority: 10", both
//     of which request immediate delivery rather than batched or deferred
//     handling. This is appropriate for user-visible notifications but
//     will be ignored by both platforms for messages that do not meet
//     their respective background-execution criteria.
//
// # Context
//
// ctx is passed to every SendAll call, so cancellation propagates into
// each in-flight HTTP round-trip. A cancellation that occurs mid-fan-out
// aborts the current chunk and causes every subsequent chunk to fail
// immediately; the results for the aborted tokens are marked "FAILED"
// with the context error attached.
//
// # Concurrency
//
// Safe for concurrent use. The underlying Firebase messaging client is
// documented as concurrency-safe; the method allocates its own results
// slice and does not share state across invocations.
func (p *FCMProvider) Send(ctx context.Context, req SendRequest) ([]SendResult, error) {
	log.Printf("[FCM] Sending notification %d to %d devices", req.NotificationID, len(req.Tokens))

	if len(req.Tokens) == 0 {
		return []SendResult{}, nil
	}

	// Pre-allocate a result slice with one slot per input token so that
	// every token — dispatched or rejected locally — occupies a fixed
	// position. The caller correlates results to input tokens by index.
	results := make([]SendResult, len(req.Tokens))

	// Build one FCM message per valid token, remembering each message's
	// original position so its per-token outcome can be written back into
	// the correct slot of the results slice.
	messages := make([]*messaging.Message, 0, len(req.Tokens))
	messageIdx := make([]int, 0, len(req.Tokens))

	for i, tokenInfo := range req.Tokens {
		if tokenInfo.PushToken == "" || len(tokenInfo.PushToken) <= minPushTokenLength {
			results[i] = SendResult{
				UserID:    tokenInfo.UserID,
				PushToken: tokenInfo.PushToken,
				Status:    "FAILED",
				Error:     fmt.Errorf("invalid push token format (empty or too short)"),
			}
			log.Printf("[FCM] Skipping invalid token for user %d", tokenInfo.UserID)
			continue
		}

		data := map[string]string{
			"notification_id": fmt.Sprintf("%d", req.NotificationID),
		}
		// The click_action is a client-side integration detail and must
		// come from the caller. Omitting the key entirely when empty
		// keeps the server free of any assumption about the mobile
		// framework in use.
		if req.ClickAction != "" {
			data["click_action"] = req.ClickAction
		}

		msg := &messaging.Message{
			Notification: &messaging.Notification{
				Title: req.Title,
				Body:  req.Body,
			},
			Token: tokenInfo.PushToken,
			Data:  data,
			Android: &messaging.AndroidConfig{
				Priority: "high",
			},
			APNS: &messaging.APNSConfig{
				Headers: map[string]string{
					"apns-priority": "10",
				},
			},
		}
		messages = append(messages, msg)
		messageIdx = append(messageIdx, i)
	}

	if len(messages) == 0 {
		// Every token failed local validation. The per-token FAILED
		// results are already populated; the function-level error is
		// returned so a caller that inspects only the error still sees
		// the failure.
		log.Printf("[FCM] Notification %d: all tokens failed local validation", req.NotificationID)
		return results, fmt.Errorf("all %d tokens failed local validation", len(req.Tokens))
	}

	totalSuccess := 0

	// Dispatch the messages in chunks that respect the SDK's per-call
	// limit. Each chunk is one HTTP round-trip; the loop is sequential.
	for start := 0; start < len(messages); start += maxFCMMessagesPerBatch {
		end := start + maxFCMMessagesPerBatch
		if end > len(messages) {
			end = len(messages)
		}
		chunk := messages[start:end]
		chunkIdx := messageIdx[start:end]

		batchResp, err := p.client.SendAll(ctx, chunk)
		if err != nil {
			// Batch-level failure for this chunk: no per-message
			// responses were produced. Every token in the chunk is
			// recorded FAILED with the batch error attached.
			log.Printf("[FCM] Batch send failed for chunk of %d messages: %v", len(chunk), err)
			for _, idx := range chunkIdx {
				results[idx] = SendResult{
					UserID:    req.Tokens[idx].UserID,
					PushToken: req.Tokens[idx].PushToken,
					Status:    "FAILED",
					Error:     err,
				}
			}
			continue
		}

		if len(batchResp.Responses) != len(chunk) {
			// The SDK contract is one response per input message, in
			// order. A mismatch here would prevent us from mapping
			// responses to tokens safely, so the whole chunk is
			// recorded FAILED rather than guessed at.
			log.Printf(
				"[FCM] Response count %d does not match chunk size %d; marking chunk failed",
				len(batchResp.Responses),
				len(chunk),
			)
			for _, idx := range chunkIdx {
				results[idx] = SendResult{
					UserID:    req.Tokens[idx].UserID,
					PushToken: req.Tokens[idx].PushToken,
					Status:    "FAILED",
					Error:     fmt.Errorf("fcm response count mismatch"),
				}
			}
			continue
		}

		for k, resp := range batchResp.Responses {
			idx := chunkIdx[k]
			tokenInfo := req.Tokens[idx]
			if resp.Success {
				results[idx] = SendResult{
					UserID:            tokenInfo.UserID,
					PushToken:         tokenInfo.PushToken,
					ProviderMessageID: resp.MessageID,
					Status:            "SENT",
				}
				log.Printf("[FCM] Sent to user %d, message ID: %s", tokenInfo.UserID, resp.MessageID)
				totalSuccess++
			} else {
				results[idx] = SendResult{
					UserID:    tokenInfo.UserID,
					PushToken: tokenInfo.PushToken,
					Status:    "FAILED",
					Error:     resp.Error,
				}
				log.Printf("[FCM] Failed for user %d: %v", tokenInfo.UserID, resp.Error)
			}
		}
	}

	if totalSuccess == 0 {
		// Every token failed, whether through local validation or
		// through an FCM send. The per-token results carry the
		// individual causes; the function-level error carries the
		// summary signal.
		log.Printf("[FCM] Notification %d: all tokens failed", req.NotificationID)
		return results, fmt.Errorf("all %d tokens failed", len(req.Tokens))
	}

	return results, nil
}
