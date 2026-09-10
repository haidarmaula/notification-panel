package staff

import "time"

// Staff represents the domain model for a staff user.
//
// It is the service-layer view of a staff_users row, joined with the role
// name so that the response views can display a human-readable role
// without a second lookup. The struct is used only within this package and
// is not serialised directly; the wire shape is StaffResponse in dto.go.
//
// # Relationship to other types in the codebase
//
// Staff is field-identical to profile.Profile and close to
// notifications.StaffBrief (which carries only ID and Name). Each feature
// declares its own view of the staff_users table rather than importing a
// canonical type; a schema change requires updating each view
// independently.
//
// # Field types
//
// All fields are non-pointer. The staff_users table has NOT NULL
// constraints on every column reflected here — the password hash is
// deliberately excluded — so the zero values of these fields correspond
// to "not populated by the database" rather than to a valid stored state.
// A caller that receives a Staff with a zero ID should treat it as an
// uninitialised value rather than as a real row.
//
// The password hash is not part of this model. Code that needs to compare
// or update a password must access the row through a repository method
// that returns the underlying sqlc type — which is what the service's
// UpdatePassword and the auth feature's Login do.
type Staff struct {
	// ID is the staff user's internal primary key.
	ID int64

	// RoleID is the internal roles.id referenced by the staff user's
	// role_id column.
	RoleID int64

	// RoleName is the resolved display name of the role, joined in at
	// query time. It reflects the current role definition, so a role
	// rename is visible here immediately.
	RoleName string

	// Name is the staff user's display name.
	Name string

	// Email is the staff user's login email.
	Email string

	// IsActive reports whether the account is enabled.
	IsActive bool

	// CreatedAt is the row's creation timestamp.
	CreatedAt time.Time

	// UpdatedAt is the row's last-modified timestamp.
	UpdatedAt time.Time
}
