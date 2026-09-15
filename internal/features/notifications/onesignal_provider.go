package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// minPushTokenLength is the threshold below which a push token is rejected
// without being forwarded to OneSignal.
//
// A token is forwarded only when its byte length is strictly greater than
// this value; a token whose PushToken is empty or whose length is less than
// or equal to minPushTokenLength is recorded in the result slice as
// "FAILED" instead. The threshold is shared with FCMProvider so that both
// providers accept and reject the same token population. Real OneSignal
// player IDs and FCM/APNs registration tokens are far longer than this
// threshold, so a token this short almost always indicates a client-side
// bug or a placeholder in test data.
//
// The value is used as an exclusive lower bound — `len(token) >
// minPushTokenLength` — so a token of exactly this many bytes is rejected.
// Callers should not depend on the specific numeric value; it is an
// implementation detail that may be adjusted without a contract change.
const minPushTokenLength = 10

// onesignalEndpoint is the OneSignal REST endpoint that this provider
// calls. It is a package-level constant rather than a struct field so that
// the URL is greppable and so that a future revision that needs to point
// at a staging endpoint has a single place to change.
const onesignalEndpoint = "https://onesignal.com/api/v1/notifications"

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
// # Error contract
//
// The provider returns a per-token result slice on every return path, and
// additionally returns a non-nil function-level error when every token
// failed. This matches the contract that FCMProvider honours after its
// H24 revision, so a caller that checks only the error return sees a
// consistent signal from both providers.
//
// The invariant a caller can rely on is:
//
//	len(results) == len(req.Tokens)
//
// on every return path, including error returns. Each entry in the slice
// carries a Status of either "SENT" or "FAILED". The Error field on a
// "FAILED" result is currently left at its zero value; the specific reason
// for each failure is logged but not surfaced through the result.
//
// # When a function-level error is returned
//
// A non-nil error is returned when and only when no token succeeded. This
// covers:
//
//   - Every token failed local validation (empty or too short).
//   - Every forwarded token failed because the batch call could not be
//     completed, was rejected by OneSignal, was accepted only partially,
//     or could not be decoded.
//
// On every error return the caller still receives the fully-populated
// result slice, so per-token detail is available alongside the summary
// signal.
//
// # Partial failure
//
// A partial failure — at least one token "SENT" and at least one "FAILED" —
// returns a nil function-level error. The caller is expected to iterate the
// result slice and inspect each Status. This asymmetry is deliberate:
// escalating every partial failure to the function-level return would
// force every caller to duplicate the per-token inspection logic.
//
// # Batch-level failures
//
// The following operational conditions are reported as per-token "FAILED"
// results with a non-nil function-level error:
//
//   - The HTTP round-trip failed: a network error, a DNS failure, or the
//     10-second client timeout elapsing.
//   - OneSignal returned a non-2xx HTTP status. The status code and a
//     bounded snippet of the response body are logged; see finding H20 in
//     code-review.md for why this check exists.
//   - The response body could not be decoded as JSON.
//   - The decoded response contained a non-empty Errors list.
//   - The decoded response reported fewer recipients than were submitted,
//     which indicates OneSignal partially accepted the batch and did not
//     identify the rejected player IDs; see finding H23 in code-review.md
//     for the reasoning behind failing the batch rather than reporting
//     partial success.
//
// A caller that needs to distinguish "the whole batch failed" from "the
// network was temporarily unavailable" must inspect its own logs; the
// current contract does not carry a failure classification.
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
// OneSignalAPIKey). Neither is validated here; an empty apiKey results in
// every OneSignal call returning 401. With the current revision, that 401
// is detected as a non-2xx status and produces per-token "FAILED" results
// plus a non-nil function-level error, rather than the silently-successful
// Send that earlier revisions emitted — see the Send documentation for the
// H20 rationale.
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
	// will be rejected by OneSignal as a 400 with a non-empty Errors list —
	// which the caller now sees as per-token "FAILED" results plus a
	// non-nil function-level error.
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
// provider decodes. OneSignal returns a richer body — including
// per-recipient error details and a "recipients" count — but only these
// three fields are read.
//
// # Relationship between ID, Recipients, and Errors
//
// On a fully successful call, ID is the notification's OneSignal-assigned
// identifier, Recipients equals the number of player IDs submitted, and
// Errors is empty.
//
// On a partially successful call, ID and Recipients are populated and
// either Errors contains one or more strings describing the rejections, or
// Recipients is smaller than the submitted count without any accompanying
// Errors entry. OneSignal does not indicate which specific player IDs were
// rejected in either case.
//
// The provider treats any of these signals as a batch-level failure and
// records per-token "FAILED" results for every forwarded token. See Send
// for the full contract and the code-review.md findings (H20, H22, H23)
// that motivated it.
type OneSignalResponse struct {
	// ID is the OneSignal-assigned identifier for the notification. On a
	// successful send, it is copied into every "SENT" SendResult's
	// ProviderMessageID — the same value for every device in the batch,
	// because OneSignal assigns one ID per notification, not per
	// recipient.
	//
	// # Per-device correlation
	//
	// The single-ID-per-notification design means that ProviderMessageID
	// alone cannot distinguish which device a delivery receipt belongs to.
	// A caller that needs per-device correlation must use the composite key
	// (ProviderMessageID, PushToken) — both are present in every SendResult
	// — or switch to a one-call-per-device strategy. The batching approach
	// this provider uses is fundamentally incompatible with per-device
	// receipt IDs, because OneSignal's batch endpoint assigns one ID to the
	// whole notification. This is finding H22 in code-review.md; the
	// limitation is documented rather than fixed because the alternative
	// — a per-device call — would forfeit the throughput advantage that is
	// this provider's reason for existing.
	ID string `json:"id"`

	// Recipients is the number of devices OneSignal accepted the
	// notification for.
	//
	// The provider compares this value against the number of player IDs it
	// submitted. A smaller value is treated as a partial rejection and
	// causes every forwarded token to be marked "FAILED", on the reasoning
	// that unknown partial state is better reported as a batch failure than
	// as an outright success — a caller that retries will over-deliver to
	// the accepted recipients, which is preferable in this domain to the
	// silent loss of the rejected ones. This is finding H23 in
	// code-review.md.
	Recipients int `json:"recipients"`

	// Errors is a list of human-readable error messages. A non-empty list
	// causes every forwarded token to be marked "FAILED"; the provider does
	// not attempt to map the errors to specific player IDs, because
	// OneSignal's response does not expose that mapping.
	Errors []string `json:"errors"`
}

// Send delivers the notification via OneSignal.
//
// # Contract
//
// On every return — nil error or non-nil error — the result slice has
// exactly one SendResult per entry in req.Tokens, in the same order as the
// input. A caller can therefore correlate result i with req.Tokens[i] by
// index, which is the pattern the delivery processor relies on during
// fan-out.
//
// Each result carries a Status of either "SENT" or "FAILED":
//
//   - "SENT" means the token was forwarded to OneSignal in the batch and
//     the batch was accepted in full. Note that OneSignal's aggregate
//     response cannot confirm per-token acceptance; see "Per-device
//     correlation" below.
//   - "FAILED" means the token was not delivered. This covers three cases:
//     the token was filtered out locally (empty or too short), the token
//     was forwarded but the batch-level request failed, or the token was
//     forwarded but the batch was accepted only partially.
//
// The Error field on a "FAILED" result is currently left at its zero value;
// the specific reason for each failure is logged but not surfaced through
// the result.
//
// # Function-level error
//
// Send returns a non-nil function-level error when and only when no token
// succeeded — either because every token failed local validation, or
// because every forwarded token failed at the batch level. Partial
// failures return a nil error; callers that need to detect them must
// iterate the result slice and inspect each Status.
//
// This alignment with FCMProvider's contract — which was updated in the
// same pass to return an error on total failure — closes the remaining
// asymmetry documented under finding H24 in code-review.md. A caller that
// checks only the error return now sees a consistent "at least one token
// succeeded" signal from both providers, and a consistent "everything
// failed" signal when the fan-out achieved nothing.
//
// # Flow
//
// The method performs at most one batched HTTP call to OneSignal's
// create-notification endpoint. Its steps are:
//
//  1. Pre-allocate a result slice of length len(req.Tokens) so that every
//     input token has a reserved position in the output regardless of how
//     many are actually forwarded.
//  2. Partition req.Tokens. Tokens whose PushToken is non-empty and longer
//     than minPushTokenLength are collected into a forwarding list, with
//     their original indices remembered. Tokens that fail either check are
//     recorded as "FAILED" in the result slice at their original position.
//  3. If no tokens survive the partition, return the result slice (which
//     contains only "FAILED" entries) and a non-nil error indicating that
//     all tokens failed local validation.
//  4. Build a OneSignalRequest containing the forwarded player IDs, the
//     request's title and body under the "en" language key, a Data map
//     carrying notification_id and click_action, and a fixed priority of
//     10.
//  5. Marshal the request. A marshal failure cannot occur with the field
//     types in use, but is handled by failing every forwarded token and
//     returning a non-nil error.
//  6. Construct the HTTP request. A construction failure cannot occur with
//     a constant endpoint, but is handled the same way as the marshal
//     failure.
//  7. POST the request and decode the response. Any failure of the round
//     trip, a non-2xx status code, a decode failure, a non-empty Errors
//     list, or a Recipients count below the submitted count all take the
//     same path: every forwarded token is marked "FAILED", the failure is
//     logged, and the method returns a non-nil error.
//  8. On full acceptance, every forwarded token is marked "SENT" with the
//     batch ID as its ProviderMessageID, and the method returns a nil
//     error.
//
// # Token filtering
//
// A token is forwarded to OneSignal only when its PushToken is non-empty
// and its byte length is strictly greater than minPushTokenLength. Tokens
// that fail either check are recorded in the result slice as "FAILED" at
// the position they occupied in req.Tokens, preserving the input ordering
// and cardinality that the delivery processor depends on.
//
// This behaviour closes finding H19 in code-review.md. A previous revision
// silently dropped short tokens from the result slice, producing an output
// whose length did not match the input and whose per-position entries no
// longer corresponded to the correct input tokens.
//
// # HTTP status inspection
//
// The response's status code is inspected before the body is decoded.
// Anything outside the 2xx range takes the batch-failure path: every
// forwarded token is marked "FAILED", the status code and up to 1 KiB of
// the response body are logged, and the method returns a non-nil error.
//
// This closes finding H20 in code-review.md. A previous revision decoded
// the response regardless of status, so a 401 Unauthorized with a
// well-formed body of `{"id":"","recipients":0,"errors":[]}` produced a
// Send that marked every token "SENT" — a silently successful return whose
// notifications never left OneSignal's servers.
//
// # Per-device correlation
//
// OneSignal assigns one notification ID per API call, not per recipient.
// The ProviderMessageID in every "SENT" result is therefore identical for
// every forwarded token in the batch. A caller that needs per-device
// receipt correlation must use the composite key (ProviderMessageID,
// PushToken) — both fields are present in every SendResult — or switch to
// a one-call-per-device strategy, which is the model FCMProvider uses at
// the cost of throughput.
//
// This is finding H22 in code-review.md. The limitation is structural: the
// batch endpoint the provider uses does not expose per-recipient IDs. The
// revision documents the composite-key workaround rather than altering the
// batching strategy.
//
// # Partial acceptance
//
// If OneSignal's response reports a Recipients count below the number of
// player IDs that were submitted, the provider treats the batch as a
// failure: every forwarded token is marked "FAILED" and a non-nil error
// is returned. This is deliberately conservative — the alternative,
// marking every token "SENT" because some were accepted, hides the fact
// that an unknown subset of recipients never received the notification.
// In the notification domain, over-delivery on a caller-triggered retry is
// preferable to silent under-delivery.
//
// This closes finding H23 in code-review.md. The provider does not attempt
// to identify which specific recipients were rejected, because OneSignal's
// response does not expose that information.
//
// # Errors
//
// A non-nil function-level error is returned on every path that leaves no
// token "SENT":
//
//   - All tokens failed local validation.
//   - The request struct failed to marshal (unreachable with the field
//     types in use).
//   - The HTTP request could not be constructed (unreachable with a
//     constant endpoint).
//   - The HTTP round-trip failed: network error, DNS failure, or the
//     10-second client timeout elapsing.
//   - OneSignal returned a non-2xx status.
//   - The response body could not be decoded as JSON.
//   - The decoded response contained a non-empty Errors list.
//   - The decoded response reported fewer recipients than were submitted.
//
// On every one of these paths the result slice is fully populated: one
// SendResult per input token, with a Status of "SENT" or "FAILED" at each
// position. This is a deliberate change from earlier revisions, which
// returned a nil slice together with the error and thereby deprived the
// caller of per-token detail. The current contract is that the result
// slice is meaningful regardless of the error return.
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

	// Pre-allocate a result slice with the same length as the input so
	// that every token — forwarded or rejected — occupies a fixed
	// position. The delivery processor correlates results to input tokens
	// by index; a shorter slice would break that correlation.
	results := make([]SendResult, len(req.Tokens))

	// Partition tokens. Forwarded tokens are collected in playerIDs and
	// their original indices are remembered in forwardedIdx; rejected
	// tokens are recorded as "FAILED" in place.
	playerIDs := make([]string, 0, len(req.Tokens))
	forwardedIdx := make([]int, 0, len(req.Tokens))

	for i, token := range req.Tokens {
		if token.PushToken == "" || len(token.PushToken) <= minPushTokenLength {
			results[i] = SendResult{
				UserID:    token.UserID,
				PushToken: token.PushToken,
				Status:    "FAILED",
			}
			continue
		}
		playerIDs = append(playerIDs, token.PushToken)
		forwardedIdx = append(forwardedIdx, i)
	}

	// failForwarded records a batch-level failure against every token
	// that was actually forwarded to OneSignal. It is invoked from each
	// failure path so that the caller always receives a per-token
	// outcome rather than a slice that silently omits tokens.
	failForwarded := func() {
		for _, idx := range forwardedIdx {
			results[idx] = SendResult{
				UserID:    req.Tokens[idx].UserID,
				PushToken: req.Tokens[idx].PushToken,
				Status:    "FAILED",
			}
		}
	}

	if len(playerIDs) == 0 {
		// Every token failed the local length check; no HTTP call is
		// attempted. The per-token FAILED results are already populated,
		// and a non-nil error is returned so a caller that inspects only
		// the error return still sees the failure.
		log.Printf("[OneSignal] No valid player IDs for notification %d", req.NotificationID)
		return results, fmt.Errorf("all %d tokens failed local validation", len(req.Tokens))
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
		// Unreachable with the field types in use. The forwarded tokens
		// are marked FAILED so that the result slice is complete on this
		// path, and the error is returned so the caller sees the defect.
		failForwarded()
		return results, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", onesignalEndpoint, bytes.NewReader(jsonBody))
	if err != nil {
		// Unreachable: the endpoint is a package-level constant. Handled
		// the same way as the marshal failure above.
		failForwarded()
		return results, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Basic "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		log.Printf("[OneSignal] Notification %d batch request failed: %v", req.NotificationID, err)
		failForwarded()
		return results, fmt.Errorf("all %d tokens failed", len(req.Tokens))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Non-2xx status. Read a bounded snippet of the body for the log
		// line, then fail the batch. The bound keeps a misbehaving
		// provider from feeding the logger an unbounded payload.
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		log.Printf(
			"[OneSignal] Notification %d non-2xx status %d: %s",
			req.NotificationID,
			resp.StatusCode,
			string(bodySnippet),
		)
		failForwarded()
		return results, fmt.Errorf("all %d tokens failed", len(req.Tokens))
	}

	var onesignalResp OneSignalResponse
	if err := json.NewDecoder(resp.Body).Decode(&onesignalResp); err != nil {
		log.Printf("[OneSignal] Notification %d decode failed: %v", req.NotificationID, err)
		failForwarded()
		return results, fmt.Errorf("all %d tokens failed", len(req.Tokens))
	}

	if len(onesignalResp.Errors) > 0 {
		log.Printf("[OneSignal] Notification %d API errors: %v", req.NotificationID, onesignalResp.Errors)
		failForwarded()
		return results, fmt.Errorf("all %d tokens failed", len(req.Tokens))
	}

	if onesignalResp.Recipients < len(playerIDs) {
		// Partial acceptance: OneSignal accepted the notification for
		// some but not all of the submitted player IDs, and did not
		// identify which ones were rejected. Failing the batch is the
		// conservative choice — the alternative would silently report
		// success for tokens that were never queued.
		log.Printf(
			"[OneSignal] Notification %d partial acceptance: %d of %d recipients",
			req.NotificationID,
			onesignalResp.Recipients,
			len(playerIDs),
		)
		failForwarded()
		return results, fmt.Errorf("all %d tokens failed", len(req.Tokens))
	}

	// Full acceptance: mark every forwarded token "SENT". The
	// ProviderMessageID is the same for all of them — see the H22 note
	// on the OneSignalResponse.ID field for the per-device correlation
	// implications.
	for _, idx := range forwardedIdx {
		token := req.Tokens[idx]
		results[idx] = SendResult{
			UserID:            token.UserID,
			PushToken:         token.PushToken,
			ProviderMessageID: onesignalResp.ID,
			Status:            "SENT",
		}
	}

	log.Printf(
		"[OneSignal] Notification %d sent to %d devices, message ID: %s",
		req.NotificationID,
		len(forwardedIdx),
		onesignalResp.ID,
	)
	return results, nil
}
