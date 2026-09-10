package profile

import "time"

// Profile represents the domain model for a staff user profile.
//
// It is the service-layer view of a staff_users row, joined with the role
// name so that the profile view can display a human-readable role without a
// second lookup. The struct is used only within this package and is not
// serialised directly; the wire shape is ProfileResponse in dto.go.
//
// # Relationship to other types in the codebase
//
// Profile is deliberately narrower than the full staff_users row: it omits
// the password hash, the account's audit metadata beyond the two
// timestamps, and any column that is not displayed in the profile view.
// This is the same pattern used by MobileUser (in the mobile feature) and
// by the auth.User model — each feature declares its own narrow view of the
// shared table rather than importing a single canonical struct.
//
// The consequence is that adding a column to staff_users requires deciding
// separately whether each of these views needs to expose it. There is no
// central schema-to-model mapping to update.
//
// # Field types
//
// All fields are non-pointer. The staff_users table has NOT NULL constraints
// on every column reflected here — the password hash is nullable in some
// schemas but is deliberately excluded from this view — so the zero values
// of these fields correspond to "not populated by the database" rather than
// to a valid stored state. A caller that receives a Profile with a zero
// ID should treat it as an uninitialised value rather than as a real row.
type Profile struct {
	// ID is the staff user's internal primary key.
	ID int64

	// RoleID is the internal roles.id referenced by the staff user's
	// role_id column. It is the FK, not a stable name.
	RoleID int64

	// RoleName is the resolved display name of the role, joined in at
	// query time. It reflects the current role definition, so a role
	// rename is visible here immediately.
	RoleName string

	// Name is the staff user's display name.
	Name string

	// Email is the staff user's login email.
	Email string

	// IsActive reports whether the account is enabled. The profile
	// handlers do not currently enforce this flag; it is surfaced for
	// display and for future policy decisions.
	IsActive bool

	// CreatedAt is the row's creation timestamp.
	CreatedAt time.Time

	// UpdatedAt is the row's last-modified timestamp.
	UpdatedAt time.Time
}
