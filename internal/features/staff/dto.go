// Package staff implements the administrative staff-management feature of
// the Notification Panel: the CRUD surface by which super-administrators
// create, inspect, modify, and deactivate other staff users of the admin
// portal.
//
// # Scope
//
// A staff user is a human operator of the admin portal — distinct from a
// mobile end user (managed by the users feature) and from the acting staff
// user's own self-service account (managed by the profile feature). The
// staff feature is administrative: it acts on other accounts. Its
// endpoints are gated by the super-admin role at the HTTP layer, and two
// service methods — Create and UpdatePassword — enforce the same
// invariant independently as defense in depth. The remaining methods
// (Update, UpdateStatus, Delete) rely on the HTTP-layer gate alone.
//
// # Relationship to auth and profile
//
// The staff feature shares the staff_users table and the bcrypt password
// store with both the auth and profile features. Its password changes take
// effect for future login attempts immediately; as with any other staff
// credential change in this codebase, they do not invalidate outstanding
// tokens. Its email-uniqueness checks share the same TOCTOU properties as
// the profile feature's UpdateProfile — the checks are advisory and the
// authoritative protection is the schema's unique constraint.
//
// # Layering
//
// The package follows the standard feature-module triad used elsewhere in
// this repository, with one structural difference:
//
//   - dto.go declares the wire shapes for HTTP requests and responses.
//   - model.go declares the domain view of a staff user.
//   - interfaces.go declares the service's dependency contracts, including
//     the local StaffServicer interface that the handler depends on.
//   - service.go implements the business logic.
//   - handler.go adapts HTTP to service calls and maps errors to status
//     codes.
//   - module.go and routes.go wire the feature into the HTTP server.
//
// The structural difference is that the handler depends on an interface
// (StaffServicer) rather than on the concrete service type. Every other
// feature module in the codebase wires the handler directly to a concrete
// *Service. See interfaces.go for the rationale.
//
// # Concurrency
//
// Every type in this package is safe for concurrent use after construction.
// The handler shares a single service implementation, which holds no
// per-request state.
package staff

// StaffResponse represents the response payload for a single staff user.
//
// It is the wire shape returned by every read endpoint in this feature
// (Create, GetByID, List, Update, UpdateStatus). It carries the identity
// columns, the resolved role name, the active flag, and the two audit
// timestamps.
//
// # Timestamp encoding
//
// CreatedAt and UpdatedAt are strings, not time.Time. The handler formats
// the underlying time.Time values to RFC3339 before assigning them via
// toStaffResponse, so the JSON wire format is identical to what
// encoding/json would emit for a time.Time field — RFC3339 with the UTC
// offset — but the Go-level type in the response struct is a string. This
// is the same pattern used by the profile feature's ProfileResponse.
//
// # Field identity with the profile feature's ProfileResponse
//
// The field set here is identical to profile.ProfileResponse. The two are
// declared separately so that they can diverge — for example, if the staff
// view later adds a "last login" column that the self-service view does
// not expose. As long as they remain identical, a future maintainer may be
// tempted to unify them; doing so would foreclose that divergence and
// should be a deliberate decision rather than a casual refactor.
type StaffResponse struct {
	// ID is the staff user's internal primary key.
	ID int64 `json:"id"`

	// RoleID is the internal roles.id the staff user's role_id column
	// references.
	RoleID int64 `json:"role_id"`

	// RoleName is the resolved role name (for example, "SUPER_ADMIN" or
	// "ADMIN"). It reflects the current role definition, not a snapshot
	// from when the staff user was created.
	RoleName string `json:"role_name"`

	// Name is the staff user's display name.
	Name string `json:"name"`

	// Email is the staff user's login email.
	Email string `json:"email"`

	// IsActive reports whether the account is enabled. A deactivated
	// staff user cannot log in, but their existing tokens remain valid
	// until they expire; see the token package for the no-revocation
	// caveat.
	IsActive bool `json:"is_active"`

	// CreatedAt is the account creation timestamp in RFC3339 format.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is the account's last-modified timestamp in RFC3339
	// format.
	UpdatedAt string `json:"updated_at"`
}

// ListStaffResponse represents the paginated list response for staff users.
//
// It is the response shape of GET /api/v1/staff. Unlike the equivalent
// responses in the segments and notifications features — which expose
// only a Total and require the client to compute the page count — this
// struct carries a pre-computed TotalPages field.
//
// # Rounding
//
// TotalPages is computed by the handler as
//
//	(total + int64(limit) - 1) / int64(limit)
//
// which is integer ceiling division. A total of 0 produces TotalPages == 0,
// matching the intuition that an empty result set has no pages.
type ListStaffResponse struct {
	// Data is the current page of staff users. An empty page is not
	// distinguishable at this level from "no staff users exist" — the
	// client must inspect Total to tell the cases apart.
	Data []StaffResponse `json:"data"`

	// Total is the total number of staff users matching the current
	// search, before pagination was applied.
	Total int64 `json:"total"`

	// Page is the 1-indexed page number. The handler defaults it to 1
	// when the query parameter is absent or unparseable.
	Page int32 `json:"page"`

	// Limit is the maximum number of items per page. The handler defaults
	// it to 10 and caps it at 100; an out-of-range client value is
	// silently replaced with the default, not rejected. The value here
	// reflects what was actually applied, not what the client asked for.
	Limit int32 `json:"limit"`

	// TotalPages is the number of pages required to hold Total items at
	// Limit per page, computed as ceiling division. A total of 0 yields 0.
	TotalPages int32 `json:"total_pages"`
}

// CreateStaffRequest represents the request payload for creating a new staff user.
//
// It is the JSON body expected by POST /api/v1/staff. All four fields are
// required; the handler rejects a request where any of them is empty with
// a 400 before the service is called.
//
// # Role is a name, not an ID
//
// The Role field carries the role's display name — for example,
// "SUPER_ADMIN" or "ADMIN" — not its numeric role_id. The service
// resolves the name to an ID via RoleRepository.FindByName before
// inserting. A name that does not resolve to a role produces
// ErrInvalidRole, served as 400.
type CreateStaffRequest struct {
	// Role is the role name to assign. It must match an existing roles
	// row's name exactly (case-sensitive).
	Role string `json:"role"`

	// Name is the new staff user's display name.
	Name string `json:"name"`

	// Email is the new staff user's login email. It is expected to be
	// unique across the table; the service checks for an existing row
	// with the same email and rejects a collision with
	// ErrEmailAlreadyRegistered, served as 409. The check is advisory —
	// see the service's Create for the concurrency caveat.
	Email string `json:"email"`

	// Password is the new staff user's plaintext password. The service
	// bcrypt-hashes it with bcrypt.DefaultCost before storing. The
	// plaintext value never leaves the request scope and must not be
	// logged.
	Password string `json:"password"`
}

// UpdateStaffRequest represents the request payload for updating a staff user.
//
// It is the JSON body expected by PATCH /api/v1/staff/{id}. Despite the
// PATCH verb, the request does not implement true partial-update semantics:
// all three fields are plain strings, so the decoder cannot distinguish
// "field omitted" from "field set to the empty string". The service uses
// the empty string as the sentinel for "leave the existing value unchanged".
//
// A caller that genuinely wants to set a field to empty cannot do so
// through this API — the request would be interpreted as a no-op for that
// field. A caller that wants to update only one field must still send a
// non-empty value for that field; the other fields can be sent as empty
// strings without effect.
//
// The handler enforces that at least one of the three is non-empty; a body
// where all three are empty is rejected with a 400. The service does not
// re-check this precondition.
type UpdateStaffRequest struct {
	// Role is the new role name. An empty string means "do not change the
	// role". A non-empty value must resolve to an existing roles row;
	// otherwise ErrInvalidRole is returned, served as 400.
	Role string `json:"role"`

	// Name is the new display name. An empty string means "do not change
	// the name".
	Name string `json:"name"`

	// Email is the new login email. An empty string means "do not change
	// the email". A non-empty value that collides with another staff
	// user's email is expected to be rejected, but note that the current
	// service implementation does not perform a per-field uniqueness
	// check on update — it passes the value to the repository, whose
	// behaviour on collision is determined by the schema's constraint.
	Email string `json:"email"`
}

// UpdateStatusRequest distinguishes an explicit false from an omitted field.
type UpdateStatusRequest struct {
	IsActive *bool `json:"is_active"`
}

// UpdatePasswordRequest represents the request payload for updating staff password.
//
// It is the JSON body expected by PATCH /api/v1/staff/{id}/password. This
// is an administrative password reset: the caller sets a new password for
// the target account. Unlike the profile feature's UpdatePasswordRequest —
// where the current password proves that the caller owns the account —
// this request requires the caller to prove that they are still the
// super-admin whose token is being used, by re-entering their *own*
// current password. The distinction reflects the two features'
// authorisation models: the profile feature is self-service, while the
// staff feature is administrative and the actor is acting on someone
// else's account.
//
// # Re-authentication rationale
//
// The CurrentPassword field is a confirmation step (sometimes called
// "sudo mode"): it forces the caller to demonstrate possession of
// something the token alone does not. A stolen super-admin token — for
// example, one exfiltrated from a browser session — is therefore not on
// its own sufficient to reset an account's password and take it over.
//
// # Plaintext handling
//
// Both fields carry plaintext values. The service bcrypt-hashes the new
// password before storing, and uses bcrypt.CompareHashAndPassword to
// verify the current password against the actor's stored hash. Neither
// plaintext value leaves the request scope, and neither must be logged.
type UpdatePasswordRequest struct {
	// CurrentPassword is the acting super-admin's own current password.
	// Required; the handler rejects an empty value with a 400 before
	// invoking the service. A value that does not match the actor's
	// stored bcrypt hash produces ErrInvalidCurrentPassword, served as
	// 403.
	CurrentPassword string `json:"current_password"`

	// Password is the new plaintext password to set on the target
	// account. Required; the handler rejects an empty value with a 400
	// before invoking the service. The service enforces a length policy
	// (minPasswordLength–maxPasswordLength) and rejects an out-of-range
	// value with ErrPasswordTooShort or ErrPasswordTooLong, both served
	// as 400.
	Password string `json:"password"`
}
