package mobile

import "time"

// MobileUser represents the domain model for a mobile user.
//
// It mirrors the columns of the users table that the mobile feature cares
// about. It is not the same type as the users feature's User — the two
// packages have overlapping but independently-declared views of the same
// table, and they must be kept in sync if the schema changes. This view is
// read-only in the mobile feature: the sync flow never writes to the users
// table, only to device_tokens.
//
// # Field naming
//
// The struct carries no JSON tags because it is never serialised to a
// client. It is the internal representation used within the service layer;
// the wire shapes are the DTOs in dto.go.
type MobileUser struct {
	// ID is the internal users.id surrogate key.
	ID int64

	// ExternalID is the identifier issued by the Laravel backend. It is the
	// authoritative key for lookups that originate from the mobile JWT.
	ExternalID string

	// Name is the user's display name, refreshed from the JWT on each sync.
	Name string

	// Email is the user's email address, refreshed from the JWT on each
	// sync. It is not used as an identity key.
	Email string

	// Status is the row's lifecycle status, as a string. The mobile feature
	// does not currently act on this field; it is carried for completeness.
	Status string

	// CreatedAt is the row's creation timestamp.
	CreatedAt time.Time

	// UpdatedAt is the row's last-modified timestamp.
	UpdatedAt time.Time
}

// MobileDevice represents the domain model for a mobile device token.
//
// It mirrors a row of the device_tokens table. Because it is the type the
// service reads and writes during sync, it is the closest thing this
// package has to a canonical shape for a device registration — but the
// authoritative columns are defined by the SQL schema and the sqlc-generated
// types, not by this struct.
//
// # Nullable timestamps
//
// LastSeenAt is a pointer, unlike the two other timestamps. This mirrors
// the schema, where last_seen_at is nullable: a device row is created
// before its first delivery attempt and only acquires a last_seen_at on
// the first successful push acknowledgment. A nil value means "never seen".
type MobileDevice struct {
	// ID is the internal device_tokens.id surrogate key.
	ID int64

	// UserID is the internal users.id the device belongs to.
	UserID int64

	// Provider is the push-provider identifier ("onesignal" or "fcm").
	Provider string

	// Platform is the client operating system ("android" or "ios").
	Platform string

	// InstallationID is the client-generated installation identifier. It
	// is a plain string here even though the request DTO uses *string, so
	// the DTO's "omitted vs. empty" distinction is not preserved at this
	// boundary.
	InstallationID string

	// PushToken is the opaque token issued by the push provider.
	PushToken string

	// AppVersion is the client app version string. As with InstallationID,
	// the request DTO's nullable form is flattened to a plain string here.
	AppVersion string

	// OSVersion is the client operating-system version string. Flattened
	// from the DTO's nullable form.
	OSVersion string

	// DeviceModel is the human-readable device model. Flattened from the
	// DTO's nullable form.
	DeviceModel string

	// IsActive reports whether the token is currently eligible for push
	// delivery. The worker filters on this flag during fan-out.
	IsActive bool

	// LastSeenAt is the most recent time the device was observed by a
	// successful delivery. Nil means the device has never had a successful
	// delivery recorded.
	LastSeenAt *time.Time

	// CreatedAt is the row's creation timestamp.
	CreatedAt time.Time

	// UpdatedAt is the row's last-modified timestamp.
	UpdatedAt time.Time
}

// MobileSyncResult represents the result of a sync operation.
//
// It is the service-layer return value from MobileService.Sync and is the
// internal counterpart of the wire-level SyncResponse. The two are
// currently field-identical; the separation exists so that the service can
// grow internal fields — for example, a `Created bool` distinguishing a
// first-time registration from an update — without altering the JSON body
// the client receives.
//
// # Field naming
//
// Like the other models in this file, MobileSyncResult carries no JSON
// tags; it is not serialised directly. The handler is responsible for
// mapping it into a SyncResponse.
type MobileSyncResult struct {
	// UserID is the internal users.id the device was associated with.
	UserID int64

	// DeviceTokenID is the internal device_tokens.id of the registered
	// device.
	DeviceTokenID int64
}
