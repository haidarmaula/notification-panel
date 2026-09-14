// Package users implements the read-side user-facing endpoints of the
// Notification Panel: the administrative views over the mobile user base
// and the device-token management surface.
//
// # Scope
//
// The users feature exposes administrative reads of the users table (list,
// search, get-by-ID), user sub-resource reads (the segments a user belongs
// to, the notifications a user has received), and CRUD over the
// device_tokens table (register, list-by-user, update, delete).
//
// It deliberately exposes no write endpoints for the users themselves.
// User rows are created and updated by the upstream Laravel backend and
// ingested via the mobile feature's sync endpoint; the users feature is
// the read surface over that data. If a future revision needs
// administrative user editing, a Create/Update/Delete triplet would be
// added here — the current absence is a deliberate design, not an
// oversight.
//
// # Mobile authentication for device-token registration
//
// The two device-token registration surfaces accept the same Laravel-issued
// identity but carry its JWT differently:
//
//   - The mobile feature's sync endpoint (POST /api/v1/mobile/sync)
//     authenticates the caller with a Laravel-issued JWT and derives the
//     user ID from the token's claims. The caller cannot register a device
//     for a user other than themselves.
//   - This feature's direct device-token endpoints
//     (POST/PATCH/DELETE /api/v1/device-tokens) authenticate with a
//     Laravel-issued bearer JWT. The authenticated external_id is resolved
//     to a local user, so callers can manage only their own tokens.
//
// The two endpoints overlap in function but differ in request shape. Both
// derive ownership from a verified Laravel JWT; see DeviceTokenHandler for
// the direct route's header-based authentication contract.
//
// # Layering
//
// The package follows the standard feature-module triad used elsewhere in
// this repository:
//
//   - dto.go declares the wire shapes for requests and responses.
//   - model.go declares the domain views of a user, a device token, a
//     user-segment membership, and a notification-history entry.
//   - service.go implements the business logic.
//   - handler.go implements the user-facing read endpoints.
//   - device_tokens_handler.go implements the device-token endpoints.
//   - module.go and routes.go wire the feature into the HTTP server.
//
// # Concurrency
//
// Every type in this package is safe for concurrent use after construction.
// The handlers share a single *UserService, which holds no per-request
// state.
package users

// ============================================
// User DTOs
// ============================================

// UserResponse represents a user in API responses.
//
// It is the wire shape returned by every read endpoint that exposes a
// user identity — List, GetByID, and the sub-resource endpoints that
// embed a user reference. It carries the user's identity columns, the
// external identifier from the upstream Laravel backend, and the two
// audit timestamps.
//
// # Timestamp encoding
//
// CreatedAt and UpdatedAt are strings, formatted by the handler to
// RFC3339 rather than relying on encoding/json's default time.Time
// marshalling. This mirrors the pattern used by the staff and profile
// features' response DTOs.
//
// # The omitempty tag on timestamps is inert
//
// Both timestamp fields carry the `omitempty` JSON tag, but the tag
// never fires: time.Time.Format(time.RFC3339) always returns a
// non-empty string, even for the zero time (which formats as
// "0001-01-01T00:00:00Z"). The tag is therefore cosmetic — a client
// cannot rely on the absence of either key to mean "unset". This is
// documented so a client parsing the response does not build logic on
// the assumption that the key may be missing.
//
// # Email is a plain string, not nullable
//
// The underlying users.email column is nullable — the repository exposes
// it as pgtype.Text — but the service flattens NULL to the empty string
// when building the domain model. A client cannot distinguish "user has
// no email" from "user's email is the empty string" through this DTO.
type UserResponse struct {
	// ID is the user's internal primary key.
	ID int64 `json:"id"`

	// ExternalID is the identifier issued by the upstream Laravel
	// backend. It is the authoritative key for cross-system correlation
	// and the identity used by the mobile sync endpoint.
	ExternalID string `json:"external_id"`

	// Name is the user's display name.
	Name string `json:"name"`

	// Email is the user's email address, or the empty string if the
	// underlying column is NULL.
	Email string `json:"email"`

	// Status is the user's lifecycle state — currently "ACTIVE" or
	// other values set by the ingestion path. The users feature does not
	// act on this field; it is exposed for display.
	Status string `json:"status"`

	// CreatedAt is the row's creation timestamp, formatted as RFC3339.
	// The omitempty tag is inert — see the type-level doc.
	CreatedAt string `json:"created_at,omitempty"`

	// UpdatedAt is the row's last-modified timestamp, formatted as
	// RFC3339. The omitempty tag is inert — see the type-level doc.
	UpdatedAt string `json:"updated_at,omitempty"`
}

// ListUsersResponse represents paginated list of users.
//
// It is the response shape of GET /api/v1/users. The Pagination field
// carries metadata about the full result set, not just the current page.
type ListUsersResponse struct {
	// Data is the current page of users. An empty page is not
	// distinguishable at this level from "no users exist" — the client
	// must inspect Pagination.Total to tell the cases apart.
	Data []UserResponse `json:"data"`

	// Pagination is the pagination metadata for the full result set.
	Pagination Pagination `json:"pagination"`
}

// SearchUserResult represents a user in search results (minimal fields).
//
// It is the per-item shape returned by GET /api/v1/users/search, a
// lightweight endpoint intended for autocomplete-style lookups. The field
// set is deliberately narrower than UserResponse: only the three fields
// an autocomplete widget needs to render a result and pass its identity
// back to a follow-up call.
//
// # No email
//
// Email is deliberately excluded, so a search response cannot be used to
// enumerate the user base's email addresses. A caller that needs the
// email must follow up with a GetByID call using the returned ID.
type SearchUserResult struct {
	// ID is the user's internal primary key.
	ID int64 `json:"id"`

	// ExternalID is the upstream identifier, included so a caller can
	// correlate the search result against Laravel-side records.
	ExternalID string `json:"external_id"`

	// Name is the user's display name.
	Name string `json:"name"`
}

// ============================================
// Device Token DTOs
// ============================================

// DeviceTokenResponse represents a device token in API responses.
//
// It is the per-item wire shape returned by the device-token list and
// register endpoints. It carries the device's identity, its push
// registration state, and the two most recent activity timestamps.
//
// # LastSeenAt is a pointer-to-string, not a time
//
// Unlike the CreatedAt field — which is always present and formatted as
// a string — LastSeenAt is a *string and is omitted entirely when nil.
// The service populates it only for tokens that have received at least
// one delivery; a freshly registered device has no last-seen timestamp
// until the first successful push. The empty-string-versus-absent
// distinction is the reason for the pointer type; see the handler's
// ListByUser method for the formatting mechanics.
//
// This is one of the few places in the codebase where a nullable
// timestamp is carried as *string rather than *time.Time; the profile,
// staff, and notifications features use the latter convention. The
// divergence is not intentional in the sense of being documented
// elsewhere — it is a consequence of this DTO having been written with
// the formatting performed in the handler before the value reaches the
// DTO.
type DeviceTokenResponse struct {
	// ID is the device_tokens row's internal primary key.
	ID int64 `json:"id"`

	// Platform is the client operating system, as recorded at
	// registration time.
	Platform string `json:"platform"`

	// InstallationID is the client-generated installation identifier.
	// It is omitted when empty.
	InstallationID string `json:"installation_id,omitempty"`

	// IsActive reports whether the token is currently eligible for
	// delivery. The worker filters on this flag during fan-out.
	IsActive bool `json:"is_active"`

	// LastSeenAt is the timestamp of the device's most recent successful
	// delivery, formatted as RFC3339, or nil (and omitted from the JSON
	// body) when the device has never been observed by a successful push.
	LastSeenAt *string `json:"last_seen_at,omitempty"`

	// CreatedAt is the row's creation timestamp, formatted as RFC3339.
	CreatedAt string `json:"created_at"`
}

// ListDeviceTokensResponse represents list of device tokens for a user.
//
// It is the response shape of GET /api/v1/users/{id}/device-tokens.
// Unlike the user list endpoint, it carries no pagination metadata: a
// user's device tokens are expected to be a small, bounded set, so the
// response returns them all in one call.
type ListDeviceTokensResponse struct {
	// Data is the full set of device tokens for the user, in the order
	// returned by the underlying query.
	Data []DeviceTokenResponse `json:"data"`
}

// ============================================
// User Segments DTO
// ============================================

// UserSegmentResponse represents a segment in user's membership.
//
// It is the per-item shape returned by GET /api/v1/users/{id}/segments,
// carrying only the two fields the user detail view displays for each
// segment. The full segment record is available from the segments
// feature's endpoints.
type UserSegmentResponse struct {
	// ID is the segment's internal primary key.
	ID int64 `json:"id"`

	// Name is the segment's display name.
	Name string `json:"name"`
}

// ListUserSegmentsResponse represents list of segments a user belongs to.
//
// It is the response shape of GET /api/v1/users/{id}/segments. As with
// the device-token list, there is no pagination — a user's segment
// memberships are expected to be a small set.
type ListUserSegmentsResponse struct {
	// Data is the full set of segments the user belongs to.
	Data []UserSegmentResponse `json:"data"`
}

// ============================================
// User Notification History DTO
// ============================================

// UserNotificationHistoryItem represents a notification in user's history.
//
// It is the per-item shape returned by GET /api/v1/users/{id}/notifications.
// It carries a compact view of the notification's identity and status,
// plus the open timestamp when the user has opened it.
//
// # OpenedAt is a pointer-to-string, not a time
//
// As with DeviceTokenResponse.LastSeenAt, the nullable timestamp is
// carried as *string rather than *time.Time. The domain model
// (UserNotification) stores it as *string directly — the value arrives
// pre-formatted from the underlying query's projection and is not parsed
// into a time.Time at any point in this feature's pipeline. A client
// decoding the JSON sees an RFC3339-shaped string when the field is
// present; the Go-level representation is a string, not a time.
//
// # Status vocabulary
//
// Status is the notification_deliveries status — "DELIVERED", "OPENED",
// "FAILED", or other values the delivery pipeline writes. The users
// feature does not define a closed enumeration; it passes the value
// through verbatim from the underlying row.
type UserNotificationHistoryItem struct {
	// NotificationID is the internal notifications.id of the entry.
	NotificationID int64 `json:"notification_id"`

	// Title is the notification's headline, as stored on the campaign.
	Title string `json:"title"`

	// Status is the delivery status for this user's copy of the
	// notification.
	Status string `json:"status"`

	// OpenedAt is the timestamp at which the user opened the
	// notification, formatted as RFC3339, or nil (and omitted) when the
	// user has not yet opened it.
	OpenedAt *string `json:"opened_at,omitempty"`
}

// ListUserNotificationsResponse represents paginated notification history.
//
// It is the response shape of GET /api/v1/users/{id}/notifications. The
// pagination convention matches the user list endpoint — metadata about
// the full result set is carried alongside the current page.
type ListUserNotificationsResponse struct {
	// Data is the current page of the user's notification history.
	Data []UserNotificationHistoryItem `json:"data"`

	// Pagination is the pagination metadata for the full history.
	Pagination Pagination `json:"pagination"`
}

// ============================================
// Device Token Registration DTOs (Mobile API)
// ============================================

// RegisterDeviceTokenRequest represents request to register a device token.
//
// It is the JSON body shape expected by two endpoints: the mobile
// feature's sync endpoint (POST /api/v1/mobile/sync), where it appears
// nested under a device_token key alongside a JWT, and this feature's
// Register endpoint (POST /api/v1/device-tokens), where it is embedded
// alongside a top-level user_id. The same struct is used in both places
// because the required fields are identical; the surrounding wrapper
// differs.
//
// # Platform values
//
// Platform must be one of "ANDROID", "IOS", or "WEB". The comparison is
// case-sensitive against those literals — the mobile feature's service
// and this feature's service both reject other values with
// ErrInvalidPlatform.
type RegisterDeviceTokenRequest struct {
	// Platform is the client operating system. Required; the caller is
	// expected to have validated its presence before constructing this
	// value, and the service rejects unrecognised values.
	Platform string `json:"platform"` // ANDROID, IOS, WEB

	// PushToken is the provider-issued token used to address the device.
	// Required; it is the natural key for idempotent registration.
	PushToken string `json:"push_token"`

	// InstallationID is an optional client-generated identifier for the
	// app installation. Nil when omitted.
	InstallationID *string `json:"installation_id,omitempty"`

	// AppVersion is the optional client app version string.
	AppVersion *string `json:"app_version,omitempty"`

	// OSVersion is the optional client operating-system version string.
	OSVersion *string `json:"os_version,omitempty"`

	// DeviceModel is the optional human-readable device model.
	DeviceModel *string `json:"device_model,omitempty"`
}

// UpdateDeviceTokenRequest represents request to update a device token.
//
// It is the JSON body expected by PATCH /api/v1/device-tokens/{id}. Every
// field is a pointer so that the handler can distinguish "field omitted"
// (nil) from "field set to a value". This gives the endpoint true PATCH
// semantics — a caller updating only the app version does not have to
// know the current platform or push token to avoid overwriting them.
//
// The handler requires at least one field to be non-nil; a request with
// every field nil is rejected with a 400 before the service is invoked.
//
// # IsActive is a *bool, not a bool
//
// Unlike the staff feature's UpdateStatusRequest — whose IsActive is a
// plain bool and cannot distinguish "omitted" from "false" — this DTO's
// IsActive is a pointer, so the two cases are distinguishable. A request
// that omits the field leaves the current status unchanged; a request
// that sets it explicitly to false deactivates the token.
type UpdateDeviceTokenRequest struct {
	// Platform, when non-nil, replaces the recorded platform. The service
	// rejects unrecognised values with ErrInvalidPlatform.
	Platform *string `json:"platform,omitempty"`

	// PushToken, when non-nil, replaces the recorded push token. A
	// non-nil value that duplicates another device token is expected to
	// be rejected with ErrDeviceTokenDuplicate, subject to the same
	// advisory-check caveat documented on the service's UpdateDeviceToken.
	PushToken *string `json:"push_token,omitempty"`

	// InstallationID, AppVersion, OSVersion, and DeviceModel are
	// optional metadata replacements. Each is applied only when non-nil.
	InstallationID *string `json:"installation_id,omitempty"`
	AppVersion     *string `json:"app_version,omitempty"`
	OSVersion      *string `json:"os_version,omitempty"`
	DeviceModel    *string `json:"device_model,omitempty"`

	// IsActive, when non-nil, replaces the token's active status. A
	// pointer so that false is distinguishable from "not provided".
	IsActive *bool `json:"is_active,omitempty"`
}

// ============================================
// Shared Pagination
// ============================================

// Pagination holds pagination metadata.
//
// It is the same struct — field for field — as the Pagination declared in
// the notifications and segments packages. Each feature declares its own
// copy rather than sharing one; a future consolidation into a shared DTO
// package would collapse the three. The trade-off of the current layout
// is that a change to the pagination contract (for example, adding a
// "has_next_page" flag) must be applied in every package that carries a
// copy.
type Pagination struct {
	// Page is the 1-indexed page number. The handler defaults to 1 when
	// the query parameter is absent or unparseable, so a legitimate
	// response never contains 0.
	Page int32 `json:"page"`

	// Limit is the maximum number of items per page. The handler defaults
	// it to 10 and caps it at 100; an out-of-range client value is
	// silently replaced with the default, not rejected. The value here
	// reflects what was actually applied, not what the client asked for.
	Limit int32 `json:"limit"`

	// Total is the total number of rows matching the current filter,
	// before pagination was applied.
	Total int64 `json:"total"`
}
