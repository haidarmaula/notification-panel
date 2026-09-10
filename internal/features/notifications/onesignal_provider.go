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

// OneSignalProvider sends notifications via OneSignal REST API.
//
// It is the production default provider. Unlike FCMProvider — which issues
// one FCM request per device token — OneSignalProvider batches every valid
// token in the request into a single HTTPS call using OneSignal's
// include_player_ids array. This makes it substantially more efficient for
// high-cardinality fan-out, at the cost of coarser error reporting: the
// OneSignal endpoint returns a single aggregate response for the batch, not
// per-token receipts, so the provider cannot distinguish which tokens in a
// partially-successful batch were accepted.
//
// # Contract divergence from FCMProvider
//
// The two providers do not honour a consistent error contract. FCMProvider
// returns per-token failures in the SendResult slice and always returns a
// nil function-level error. OneSignalProvider returns a nil, non-nil error
// pair on any failure — including failures of a single request inside what
// would otherwise be a batch — and does not populate per-token results on
// those paths.
//
// Callers written against one provider's semantics will mishandle the
// other. The correct fix is to standardise on one shape — either per-token
// results with a nil function-level error, or a strictly-typed error return
// — across every implementation of NotificationProvider. Until then, the
// safest pattern for callers is to check both return values: a non-nil
// error means the batch failed entirely, a non-nil slice means at least
// some tokens were dispatched.
//
// # Concurrency
//
// The embedded http.Client is documented by the standard library as safe
// for concurrent use, and the provider holds no other mutable state.
// Concurrent Send calls are safe.
type OneSignalProvider struct {
	appID  string
	apiKey string
	client *http.Client
}

// NewOneSignalProvider creates a new OneSignalProvider with a 10s timeout.
//
// # Parameters
//
// appID and apiKey are sourced from config.WorkerConfig (OneSignalAppID and
// OneSignalAPIKey). Neither is validated; an empty apiKey will result in
// every OneSignal call returning 401, which the provider does not detect as
// a distinct error — see Send for how the 401 body is (mis)handled.
//
// # Client configuration
//
// The embedded http.Client uses a 10-second whole-request timeout. Because
// OneSignal requests are batched — potentially carrying thousands of player
// IDs — a timeout at 10 seconds is generous for small batches and
// potentially tight for very large ones. There is no separate dial timeout
// or idle-connection tuning; the defaults from net/http apply.
//
// The client does not follow a custom retry policy, does not inject request
// IDs, and does not instrument outgoing calls with tracing. Each Send
// invocation performs exactly one HTTP round-trip, regardless of how many
// tokens are in the batch.
func NewOneSignalProvider(appID, apiKey string) *OneSignalProvider {
	return &OneSignalProvider{
		appID:  appID,
		apiKey: apiKey,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// OneSignalRequest represents the payload for OneSignal's create notification endpoint.
//
// The shape mirrors the subset of OneSignal's REST API that this provider
// uses. Fields not present here — for example, scheduled delivery, rich
// media attachments, or notification categories — are not supported by the
// current implementation; extending the API surface requires adding fields
// to this struct and populating them in Send.
//
// # Content maps
//
// Headings and Contents are modelled as language-keyed maps
// (map[string]string), which is OneSignal's representation for
// localised content. This provider only ever sets the "en" key; the
// structure is preserved for schema fidelity but no localisation is
// actually performed.
type OneSignalRequest struct {
	// AppID is the OneSignal application identifier, taken from the
	// provider's configuration. It is a required field for every OneSignal
	// call.
	AppID string `json:"app_id"`

	// IncludePlayerIDs is the batch of device tokens to deliver to.
	// OneSignal's API accepts up to 2,000 player IDs per request; the
	// provider does not enforce that limit, so a batch larger than 2,000
	// will be rejected by OneSignal with an API error.
	IncludePlayerIDs []string `json:"include_player_ids"`

	// Headings is the notification title, keyed by language code. The
	// provider sets only the "en" key.
	Headings map[string]string `json:"headings"`

	// Contents is the notification body, keyed by language code. The
	// provider sets only the "en" key.
	Contents map[string]string `json:"contents"`

	// Data is an arbitrary key/value payload delivered to the client
	// alongside the notification. The provider populates it with
	// "notification_id" (as an int64, not a string — OneSignal serialises
	// it as a JSON number) and "click_action".
	Data map[string]any `json:"data,omitempty"`

	// AndroidChannelID is the notification channel for Android 8+. The
	// provider declares this field for schema completeness but never sets
	// it; a deployment that requires a specific Android channel must extend
	// Send to populate it.
	AndroidChannelID string `json:"android_channel_id,omitempty"`

	// Priority is OneSignal's priority hint, where 10 requests high
	// priority. The provider hardcodes 10, matching the intent of the
	// FCMProvider's AndroidConfig.Priority setting.
	Priority int `json:"priority,omitempty"`
}

// OneSignalResponse is the API response structure.
//
// It is the subset of OneSignal's create-notification response that this
// provider decodes. OneSignal returns a richer body — including per-recipient
// error details and a "recipients" count — but only these three fields are
// read.
//
// # Relationship between ID, Recipients, and Errors
//
// On a fully successful call, ID is the notification's OneSignal-assigned
// identifier, Recipients is the number of devices the notification was
// accepted for, and Errors is empty.
//
// On a partially successful call, ID and Recipients are populated and
// Errors contains one or more strings describing the rejections (for
// example, invalid player IDs). OneSignal does not, however, indicate which
// specific player IDs were rejected.
//
// The provider's handling of this structure is documented on Send; in
// particular, note that Recipients is not used to filter the result set.
type OneSignalResponse struct {
	// ID is the OneSignal-assigned identifier for the notification. It is
	// copied into every SendResult's ProviderMessageID — the same value for
	// every device in the batch, since OneSignal assigns one ID per
	// notification, not per recipient.
	ID string `json:"id"`

	// Recipients is the number of devices OneSignal accepted the
	// notification for. The provider does not currently consult this field;
	// see Send for the consequence.
	Recipients int `json:"recipients"`

	// Errors is a list of human-readable error messages. If non-empty, the
	// provider treats the whole request as failed — even though OneSignal
	// may have accepted the notification for a subset of recipients. See
	// Send for the implication.
	Errors []string `json:"errors"`
}

// Send delivers the notification via OneSignal.
// It skips tokens that are empty or too short, and returns a SendResult per device.
//
// # Flow
//
// The method performs a single batched call to OneSignal's create-
// notification endpoint. Its steps are:
//
//  1. Filter req.Tokens, keeping only tokens whose PushToken is non-empty
//     and longer than 10 bytes. Tokens shorter than or equal to 10 bytes
//     are silently dropped — see the "Silently dropped tokens" section.
//  2. If no tokens survive the filter, return an empty result slice and a
//     nil error, without contacting OneSignal.
//  3. Build a OneSignalRequest containing the filtered player IDs, the
//     request's title and body under the "en" language key, a Data map
//     carrying notification_id and click_action, and a fixed priority of
//     10.
//  4. Marshal the request, POST it to https://onesignal.com/api/v1/notifications
//     with the app's apiKey in a Basic-auth header, and decode the
//     response body as a OneSignalResponse.
//  5. If the decoded response has a non-empty Errors list, return a nil
//     result slice and an error whose message is the errors joined.
//  6. Otherwise, return one SendResult per surviving token, every one of
//     them with Status "SENT", the response's ID as ProviderMessageID, and
//     a nil Error.
//
// # Silently dropped tokens
//
// The length filter at step 1 removes tokens the provider considers
// obviously invalid. Because those tokens do not appear in the output
// slice, the length of the returned slice may be smaller than the length
// of req.Tokens. A caller that expects a one-to-one mapping — as it would
// get from FCMProvider — will mis-correlate results to inputs. In
// particular, the delivery processor's fan-out loop indexes into
// req.Tokens by result position in some code paths; correctness depends on
// the provider preserving the input ordering and cardinality, which
// OneSignalProvider does not.
//
// There is no result entry with Status "FAILED" for the dropped tokens, so
// the caller has no way to record a delivery for them. They are not
// delivered and not tracked.
//
// # Single-ID response semantics
//
// OneSignal assigns one notification ID per API call, not per recipient.
// The ProviderMessageID in every result is therefore identical for every
// device in the batch. A caller that needs per-device receipt correlation
// cannot get it from this provider without switching to one call per
// device — which is the model FCMProvider uses, at the cost of throughput.
//
// # Recipients not consulted
//
// The response's Recipients field, which records how many devices OneSignal
// accepted the notification for, is not read. When OneSignal partially
// rejects a batch (Errors is empty but Recipients is smaller than the
// number of submitted player IDs), the provider still marks every surviving
// token as "SENT". Callers that need an accurate count of accepted
// recipients must not rely on the result slice.
//
// # HTTP status not checked
//
// The method does not inspect resp.StatusCode. A 401 Unauthorized response
// with a valid JSON body — for example, {"id":"","recipients":0,"errors":[]}
// — will be decoded successfully and, because Errors is empty, every
// surviving token will be marked "SENT". A misconfigured apiKey therefore
// produces a silently successful Send whose notifications never leave the
// provider's servers. The only way this provider surfaces an
// authentication problem is if OneSignal's error body happens to include
// a non-empty Errors array for the 401, which is not guaranteed.
//
// # Errors
//
// A non-nil error is returned in four cases:
//
//   - The request struct fails to marshal as JSON. This is unexpected for
//     the field types involved and indicates a code defect.
//   - The HTTP request cannot be constructed. Also unexpected.
//   - The HTTP round-trip fails — network error, DNS failure, or the
//     10-second client timeout elapsing.
//   - The response body cannot be decoded as JSON, or the decoded response
//     contains a non-empty Errors list.
//
// On every error path, the result slice is nil. The method does not
// distinguish transient failures (network, timeout) from permanent ones
// (invalid credentials, malformed request); a caller implementing retry
// logic must classify the error itself.
//
// # Context
//
// ctx is passed to http.NewRequestWithContext, so cancellation propagates
// into the HTTP round-trip. The 10-second client timeout applies on top of
// any shorter deadline in ctx; whichever elapses first wins.
//
// # Concurrency
//
// Safe for concurrent use. The provider holds no mutable state beyond the
// embedded http.Client, which is documented as concurrency-safe.
func (p *OneSignalProvider) Send(ctx context.Context, req SendRequest) ([]SendResult, error) {
	log.Printf("[OneSignal] Sending notification %d to %d devices", req.NotificationID, len(req.Tokens))

	if len(req.Tokens) == 0 {
		return []SendResult{}, nil
	}

	// Map player IDs to user IDs for result correlation.
	playerIDs := make([]string, 0, len(req.Tokens))
	userIDMap := make(map[string]int64)

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

	onesignalReq := OneSignalRequest{
		AppID:            p.appID,
		IncludePlayerIDs: playerIDs,
		Headings:         map[string]string{"en": req.Title},
		Contents:         map[string]string{"en": req.Body},
		Data: map[string]any{
			"notification_id": req.NotificationID,
			"click_action":    req.ClickAction,
		},
		Priority: 10,
	}

	jsonBody, err := json.Marshal(onesignalReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

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
