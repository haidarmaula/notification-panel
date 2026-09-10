package profile

// ProfileResponse represents the response payload for a staff profile.
//
// It is the wire shape returned by the two read/write endpoints that expose
// the acting staff user's account (GET /api/v1/profile and
// PATCH /api/v1/profile). It carries the identity columns, the resolved
// role name, the active flag, and the two audit timestamps.
//
// # Timestamp encoding
//
// CreatedAt and UpdatedAt are strings, not time.Time, unlike every other
// response DTO in this codebase. The handler formats the underlying
// time.Time values to RFC3339 before assigning them, so the JSON wire
// format is identical to what encoding/json would produce for a time.Time
// field — RFC3339 with the UTC offset — but the Go-level type in the
// response struct is a string. A caller that decodes into a Go struct of
// this shape receives a string; a caller that decodes into a struct with
// time.Time fields receives the parsed value. Both work, but the asymmetry
// with the rest of the codebase is worth knowing when writing client code
// or server-side tests.
//
// The two fields are always populated — the Profile model never has a
// zero-value CreatedAt or UpdatedAt — so neither is a pointer.
type ProfileResponse struct {
	// ID is the staff user's internal primary key.
	ID int64 `json:"id"`

	// RoleID is the internal role identifier the staff user's role_id
	// column references. It is the FK, not a stable name.
	RoleID int64 `json:"role_id"`

	// RoleName is the resolved role name (for example, "SUPER_ADMIN" or
	// "ADMIN"). The service joins the roles table to obtain it, so the
	// value reflects the current role definition, not a snapshot from
	// when the staff user was created.
	RoleName string `json:"role_name"`

	// Name is the staff user's display name.
	Name string `json:"name"`

	// Email is the staff user's login email.
	Email string `json:"email"`

	// IsActive reports whether the account is currently enabled. A
	// deactivated user can still read and update their profile in the
	// current implementation — the profile handlers do not check this
	// flag — but cannot log in.
	IsActive bool `json:"is_active"`

	// CreatedAt is the account creation timestamp in RFC3339 format.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is the account's last-modified timestamp in RFC3339
	// format.
	UpdatedAt string `json:"updated_at"`
}

// UpdateProfileRequest represents the request payload for updating a profile.
//
// It is the JSON body expected by PATCH /api/v1/profile. Despite the PATCH
// verb, the request does not implement partial-update semantics: both
// fields are plain strings, so the decoder cannot distinguish "field
// omitted" from "field set to the empty string". The handler enforces that
// at least one of the two is non-empty, but a request that supplies only
// Name will pass Email as an empty string to the service.
//
// Whether an empty Email is treated as "leave the existing email alone" or
// "set the email to empty" is determined by the service's implementation.
// Callers building a partial-update client should therefore send both
// fields on every request, using the values currently held by the server
// for whichever field they do not intend to change. A future revision that
// wants true partial-update semantics should change these fields to
// *string, matching the pattern used by UpdateNotificationRequest in the
// notifications feature.
type UpdateProfileRequest struct {
	// Name is the new display name. An empty value is not rejected
	// individually, but a request where both Name and Email are empty is
	// rejected with 400 by the handler.
	Name string `json:"name"`

	// Email is the new login email. An empty value is not rejected
	// individually. A non-empty value that collides with another staff
	// user's email is rejected with ErrEmailAlreadyUsed, served as 409.
	Email string `json:"email"`
}

// UpdatePasswordRequest represents the request payload for updating password.
//
// It is the JSON body expected by PATCH /api/v1/profile/password. Both
// fields are required; the handler rejects a request with either field
// empty before calling the service.
//
// # Plaintext handling
//
// Both fields carry plaintext passwords. CurrentPassword is compared
// against the stored bcrypt hash and is never persisted. NewPassword is
// bcrypt-hashed by the service before being written. Neither value should
// be logged, and the struct should not be serialised to any persistent
// store. The pattern mirrors LoginRequest in the auth feature.
type UpdatePasswordRequest struct {
	// CurrentPassword is the caller's existing password, supplied as proof
	// of identity for the change. The service compares it against the
	// stored hash; a mismatch produces ErrInvalidCredentials, mapped by
	// the handler to 401.
	CurrentPassword string `json:"current_password"`

	// NewPassword is the desired replacement, in plaintext. The service
	// applies the same bcrypt hashing used elsewhere in the codebase; no
	// password-strength or complexity check is performed at this layer or
	// (as far as this package is aware) at any other.
	NewPassword string `json:"new_password"`
}
