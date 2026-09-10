package notifications

import "context"

// NotificationProvider defines the interface for sending push notifications
// to device tokens. Implementations may use OneSignal, FCM, or other services.
//
// # Contract
//
// The interface is intentionally minimal: a single method that takes a
// SendRequest carrying the notification content and a batch of device
// tokens, and returns a slice of SendResult — one per token — plus a
// function-level error.
//
// The contract callers (currently Processor.ProcessSendRequested) rely on
// is the following, though the shipped implementations do not all honour
// it:
//
//   - One SendResult is returned per input token, in the same order as
//     req.Tokens. The caller correlates results to tokens by position and
//     by UserID.
//   - A per-token failure is reported as a SendResult with Status "FAILED"
//     and a non-nil Error field. It is not reported through the
//     function-level error.
//   - The function-level error is reserved for failures that prevent any
//     result from being produced at all — a transport failure, an
//     authentication failure, or a malformed request that the provider
//     rejects before attempting delivery.
//
// # Current implementations and their divergences
//
// Three implementations ship in this package: MockProvider, FCMProvider,
// and OneSignalProvider. Only the first two honour the contract above:
//
//   - MockProvider returns exactly one result per input token, all
//     "SENT", all with a non-nil Error field of nil. Its function-level
//     error is always nil.
//   - FCMProvider honours the per-token contract, but its function-level
//     error is also always nil — even when the provider's internal SDK
//     reports a failure for every token.
//   - OneSignalProvider does not honour the per-token contract. It filters
//     out tokens whose PushToken is empty or shorter than 10 bytes before
//     dispatching, so the returned slice may be shorter than the input,
//     and it returns a nil result slice plus a non-nil error on any batch
//     failure — including one caused by a single bad player ID.
//
// Callers written against one provider's semantics will mishandle the
// others. The safe pattern is to treat the interface as documentation of
// intent, not as a guarantee of behaviour, and to consult each
// implementation's Godoc before relying on any specific return-shape
// property.
//
// # Concurrency
//
// Implementations are expected to be safe for concurrent Send calls. All
// three shipped providers meet this expectation.
type NotificationProvider interface {
	// Send dispatches the notification described by req to the devices in
	// req.Tokens. See the interface documentation for the result-shape
	// contract and the divergences among the shipped implementations.
	Send(ctx context.Context, req SendRequest) ([]SendResult, error)
}

// DeviceTokenInfo holds the essential data for a single device token.
//
// It is the provider-facing projection of a device_tokens row: the three
// fields a provider needs in order to address a delivery and attribute its
// result. Fields that exist on the row but are not needed for dispatch —
// the installation ID, the client app version, the last-seen timestamp —
// are deliberately absent so that the SendRequest payload stays small even
// for high-cardinality fan-outs.
//
// DeviceTokenInfo is a plain data struct with no internal locking and is
// not meant to be shared across goroutines without external
// synchronisation. In practice it is constructed on the stack inside
// Processor.ProcessSendRequested and handed to the provider by value.
type DeviceTokenInfo struct {
	// UserID is the internal users.id the device belongs to. It is
	// carried for two reasons: to allow the caller to correlate a
	// SendResult back to a user when the provider does not echo the token
	// (OneSignal), and to allow the provider to attach the ID to each
	// result for downstream delivery recording.
	UserID int64

	// PushToken is the provider-issued token used to address the device.
	// Its format depends on the platform and provider: FCM tokens and
	// APNs tokens are both opaque strings of substantial length. The
	// shipped providers apply a length heuristic (empty or shorter than
	// 10 bytes) to reject obviously-invalid values before dispatching.
	PushToken string

	// Platform is the client operating system as recorded at registration
	// time. It is not used by any shipped provider for dispatch
	// decisions — the token itself encodes the platform — but is carried
	// for diagnostic logging and for future provider-specific routing.
	Platform string
}

// SendRequest encapsulates notification content and target devices.
//
// It is the provider-agnostic description of a single dispatch: one
// notification's content delivered to a set of devices. The struct is
// produced by Processor.ProcessSendRequested after resolving the
// notification's targets and expanding them into device tokens.
//
// # Scope
//
// A SendRequest is a one-shot batch. It does not carry retry metadata, a
// correlation ID beyond the notification ID, or per-device overrides. If a
// caller needs to deliver different content to different devices, it must
// issue multiple SendRequests — the interface does not support
// heterogeneous batches.
type SendRequest struct {
	// NotificationID is the internal notifications.id being delivered. It
	// is passed through to the provider so that any provider-side
	// attribution (message tags, click-action routing, analytics
	// correlation) can reference the same identifier the rest of the
	// system uses.
	NotificationID int64

	// Title is the notification's headline, ready to be rendered by the
	// device's OS notification UI. No templating or interpolation is
	// applied by the providers; the caller is expected to have resolved
	// any placeholders before constructing the request.
	Title string

	// Body is the notification's textual content, rendered alongside the
	// title. As with Title, no substitution is performed by the providers.
	Body string

	// Tokens is the batch of devices to deliver to. The order is
	// significant: providers that return a result slice are expected to
	// return it in the same order, so the caller can correlate by
	// position. An empty slice is a valid input and should produce an
	// empty result slice rather than an error.
	Tokens []DeviceTokenInfo

	// ClickAction is the client-side action identifier emitted alongside
	// the notification so that the app can route a tap to the right
	// screen. The exact semantics depend on the client platform: on
	// Android via FCM it may be interpreted as a deep-link action; on
	// iOS via APNs it is transmitted in the payload's data dictionary.
	// OneSignal and FCM both forward this value verbatim, so the string
	// must match whatever the mobile client expects — the current value
	// used by the processor is "NOTIFICATION_CLICK".
	ClickAction string
}

// SendResult represents the outcome for a single device.
//
// One SendResult is expected per input DeviceTokenInfo, though the
// OneSignalProvider's token-filtering behaviour can produce fewer — see
// NotificationProvider for the full contract divergence.
//
// # Status values
//
// The two providers that populate Status use the same two literals:
//
//   - "SENT" — the provider accepted the notification for this device.
//   - "FAILED" — the provider rejected it, and the Error field describes
//     why.
//
// No other values are produced by the shipped providers. A caller that
// wants to persist the outcome should treat the Status field as the
// source of truth and use the Error field only for the failure reason.
type SendResult struct {
	// UserID echoes the UserID from the corresponding DeviceTokenInfo so
	// that the caller can record the outcome without re-correlating by
	// position. It is expected to match the input exactly; a provider
	// that returns a different value has broken the contract.
	UserID int64

	// PushToken echoes the token from the corresponding DeviceTokenInfo.
	// For providers that return a per-device result, this identifies
	// which device the outcome belongs to; for OneSignal's batched
	// response, every result carries the same ProviderMessageID but a
	// distinct PushToken, so the token remains the only per-device
	// discriminator.
	PushToken string

	// ProviderMessageID is the provider-assigned identifier for the
	// delivery, used for cross-referencing the delivery against
	// provider-side logs and receipts.
	//
	// Its granularity varies by provider. FCM returns one message ID per
	// device, so each result carries a distinct value. OneSignal returns
	// one notification ID per API call, so every result in a batch
	// carries the same value. Callers must not assume the ID is unique
	// across results.
	ProviderMessageID string

	// Status is the outcome for this device. See the type-level doc for
	// the two literal values in use and their meaning.
	Status string

	// Error is non-nil for a failed delivery and describes the failure.
	// It is nil for a successful one. The error is the provider's own —
	// it may be an SDK error (FCM) or a formatted message constructed by
	// the provider (OneSignal) — and it is not wrapped in any sentinel,
	// so callers cannot use errors.Is to distinguish failure modes.
	Error error
}
