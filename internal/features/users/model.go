package users

import "time"

// User represents the domain model for a mobile application user.
//
// It is the service-layer view of a users row. The struct is used only
// within this package and is not serialised directly; the wire shape is
// UserResponse in dto.go.
//
// # Relationship to mobile.MobileUser
//
// This struct is field-identical to mobile.MobileUser — the two features
// have independently-declared views of the same users table. Each
// declares its own type rather than sharing one, on the principle that a
// feature's domain model reflects what that feature cares about. The
// practical consequence is that adding a column to users requires a
// decision, applied separately, in each view; there is no central
// schema-to-model mapping to update.
//
// # Field types
//
// All fields are non-pointer, and none is nullable except through the
// service's flattening: the underlying users.email column is nullable at
// the schema level (pgtype.Text in the repository layer), but this
// struct declares it as a plain string, so a NULL email becomes the
// empty string before reaching the domain layer. The same is true for
// Name. Callers cannot distinguish "no name" from "empty name" through
// this struct.
type User struct {
	// ID is the user's internal primary key.
	ID int64

	// ExternalID is the identifier issued by the upstream Laravel
	// backend. It is the authoritative key for cross-system correlation.
	ExternalID string

	// Name is the user's display name, or the empty string when the
	// underlying column is NULL.
	Name string

	// Email is the user's email address, or the empty string when the
	// underlying column is NULL.
	Email string

	// Status is the user's lifecycle state. The users feature does not
	// act on this value; it is carried for display.
	Status string

	// CreatedAt is the row's creation timestamp.
	CreatedAt time.Time

	// UpdatedAt is the row's last-modified timestamp.
	UpdatedAt time.Time
}

// DeviceToken represents the domain model for a device token.
//
// It is the service-layer view of a device_tokens row. As with the other
// domain models in this package, it is not serialised directly — the wire
// shape is DeviceTokenResponse.
//
// # Relationship to mobile.MobileDevice
//
// This struct is field-identical to mobile.MobileDevice. The same
// duplicate-declaration observation documented on User applies here.
//
// # LastSeenAt is nullable
//
// The only pointer field on the struct. It mirrors the nullable
// device_tokens.last_seen_at column: a device that has never been
// observed by a successful delivery has a nil value. See the
// corresponding DeviceTokenResponse field for the wire-level
// representation.
//
// # Flattened optional fields
//
// InstallationID, AppVersion, OSVersion, and DeviceModel are declared as
// plain strings here even though the request DTOs carry them as *string
// — the nullable representation is used for request parsing, and the
// domain layer treats NULL and empty-string as equivalent. A NULL column
// becomes an empty string; a client cannot distinguish the two through
// this model.
type DeviceToken struct {
	// ID is the device_tokens row's internal primary key.
	ID int64

	// UserID is the internal users.id the device belongs to.
	UserID int64

	// Provider is the push provider identifier, such as "ONESIGNAL" or
	// "FCM".
	Provider string

	// Platform is the client operating system, as recorded at
	// registration time.
	Platform string

	// InstallationID is the client-generated installation identifier, or
	// the empty string if not recorded.
	InstallationID string

	// PushToken is the provider-issued token used to address the device.
	PushToken string

	// AppVersion is the client app version string, or the empty string
	// if not recorded.
	AppVersion string

	// OSVersion is the client operating-system version string, or the
	// empty string if not recorded.
	OSVersion string

	// DeviceModel is the human-readable device model, or the empty
	// string if not recorded.
	DeviceModel string

	// IsActive reports whether the token is eligible for delivery.
	IsActive bool

	// LastSeenAt is the time of the device's most recent successful
	// delivery, or nil if the device has never been observed.
	LastSeenAt *time.Time

	// CreatedAt is the row's creation timestamp.
	CreatedAt time.Time

	// UpdatedAt is the row's last-modified timestamp.
	UpdatedAt time.Time
}

// UserSegment represents a segment that a user belongs to.
//
// It is a minimal reference used by the user detail view's segment list:
// only the segment's identity and display name are carried, on the
// assumption that a caller needing the full segment record will fetch it
// from the segments feature's endpoints.
type UserSegment struct {
	// ID is the segment's internal primary key.
	ID int64

	// Name is the segment's display name.
	Name string
}

// UserNotification represents a notification in user's history.
//
// It is the service-layer view of a joined notifications-and-deliveries
// row, filtered to a single user. It is not serialised directly; the
// wire shape is UserNotificationHistoryItem.
//
// # OpenedAt is a pointer-to-string, not a time
//
// Unlike every other domain model's timestamp field in the codebase,
// OpenedAt is declared as *string rather than *time.Time. The value
// arrives pre-formatted from the underlying query's projection and is
// passed through to the DTO without parsing. The reason for this
// representation is not documented in the code; it may be a historical
// artefact. A future refactor to *time.Time would bring this model into
// line with the rest of the codebase, at the cost of moving the
// formatting to the handler layer.
//
// # No notification body
//
// The struct does not carry the notification's Body field — only the
// title. A client that needs the body must fetch it from the
// notifications feature's detail endpoint.
type UserNotification struct {
	// NotificationID is the internal notifications.id of the entry.
	NotificationID int64

	// Title is the notification's headline.
	Title string

	// Status is the delivery status for this user's copy of the
	// notification. It is passed through verbatim from the underlying
	// row; the users feature does not define a closed enumeration.
	Status string

	// OpenedAt is the timestamp at which the user opened the
	// notification, formatted as a string, or nil if the user has not
	// yet opened it. See the type-level doc for the representation
	// caveat.
	OpenedAt *string
}
