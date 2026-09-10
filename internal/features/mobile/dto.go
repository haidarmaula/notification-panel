package mobile

// DeviceTokenRequest represents the device token part of the sync request.
//
// It carries the device's push-registration state as reported by the mobile
// client on each sync call. The four optional fields use pointer types so
// that the JSON decoder can distinguish "field omitted from the request"
// (nil) from "field explicitly present with an empty value" (&"") — a
// distinction that is invisible to the mobile client but matters for the
// server, which uses it to decide whether to overwrite existing values on
// the device_tokens row.
//
// The two required fields — Platform and PushToken — are plain strings;
// their presence is enforced by the handler rather than by struct tags.
type DeviceTokenRequest struct {
	// Platform identifies the client operating system. The service expects
	// a value it recognises — currently "android" or "ios" — and rejects
	// anything else with ErrInvalidPlatform, surfaced as a 400 by the
	// handler. Case sensitivity is determined by the service's comparison;
	// see its documentation for the exact contract.
	Platform string `json:"platform"`

	// PushToken is the opaque token issued by the push provider (FCM or
	// APNs) and used by the delivery worker to address this device. It is
	// the natural key for idempotent registration: two syncs from the same
	// device carry the same PushToken, and the service treats them as the
	// same registration.
	PushToken string `json:"push_token"`

	// InstallationID is an optional client-generated identifier for the
	// app installation. It is nil when the client omits it; on the wire it
	// is omitted entirely rather than sent as an empty string.
	InstallationID *string `json:"installation_id,omitempty"`

	// AppVersion is the optional client app version string. It is nil when
	// the client omits it and is used for diagnostic grouping in the admin
	// portal, not for any behavioural decision.
	AppVersion *string `json:"app_version,omitempty"`

	// OSVersion is the optional client operating-system version string.
	// Nil when omitted; used for diagnostic grouping only.
	OSVersion *string `json:"os_version,omitempty"`

	// DeviceModel is the optional human-readable device model. Nil when
	// omitted; used for diagnostic grouping only.
	DeviceModel *string `json:"device_model,omitempty"`
}

// SyncRequest represents the full sync request payload.
//
// It is the JSON body expected by POST /api/v1/mobile/sync. The structure
// deliberately nests the device information under a device_token object so
// that future additions to the sync payload — for example, a client-side
// settings object — can be added as sibling fields without breaking the
// existing schema.
//
// # JWT placement
//
// The JWT is carried in the request body rather than in the Authorization
// header. See the package documentation for the rationale; briefly, the
// mobile client is exchanging a Laravel-issued token for a device
// registration, and the request is not an authenticated session call in the
// sense that staff-facing endpoints are.
type SyncRequest struct {
	// JWT is the token issued by the Laravel mobile backend. It must be
	// present and non-empty; the handler rejects an empty value with a 400
	// before the service is invoked. Signature verification and expiry
	// checking are performed by the service against MOBILE_JWT_SECRET.
	JWT string `json:"jwt"`

	// DeviceToken carries the device's push-registration state. Both
	// required fields within it — Platform and PushToken — are validated
	// by the handler before the service is invoked.
	DeviceToken DeviceTokenRequest `json:"device_token"`
}

// SyncResponse represents the response after sync.
//
// It is the JSON body returned on a successful sync. The two IDs it carries
// identify the persisted state so that the mobile client can log them and
// correlate against server-side records if troubleshooting is required;
// the client does not otherwise need them to operate.
//
// This struct is deliberately separate from MobileSyncResult, the
// service-layer result. The two currently have identical fields, but
// keeping them distinct allows the service to add internal fields — for
// example, a `Created bool` to signal first-time registration — without
// changing the wire contract.
type SyncResponse struct {
	// UserID is the internal users.id of the user the device was associated
	// with. Note that this is not the Laravel external_id from the JWT; it
	// is the local surrogate key used by every other table in this service.
	UserID int64 `json:"user_id"`

	// DeviceTokenID is the internal device_tokens.id of the registered
	// device. It is stable across syncs for the same (user, push_token)
	// pair.
	DeviceTokenID int64 `json:"device_token_id"`
}
