package auth

// User represents a staff user in the authentication context.
// This is a minimal view used for login and token generation.
//
// It is intentionally narrower than sqlc.StaffUser: only the three fields
// required to verify a password and mint a token are carried, so that the
// auth service cannot accidentally depend on unrelated staff metadata
// (role timestamps, audit columns, status flags beyond active/inactive).
//
// # Password handling
//
// Despite the field name, Password holds the *stored bcrypt hash*, not the
// plaintext supplied by the client. The naming reflects the historical
// shape of the type, not its contents. Two practical consequences follow:
//
//  1. Code that compares a plaintext password against this field must use
//     bcrypt.CompareHashAndPassword, not a direct equality check. Any
//     future caller that assumes plaintext will silently reject every valid
//     login.
//  2. Because the struct is exported and the field carries a
//     `json:"password"` tag, encoding a User to JSON will serialise the
//     bcrypt hash into the response. Handlers must never pass a User (or
//     anything embedding one) to response.JSON. The auth handler does not
//     do so today, but the tag makes the mistake easy to make accidentally;
//     a future revision should consider renaming the field to PasswordHash
//     or tagging it `json:"-"`.
type User struct {
	// ID is the internal staff_users.id. It is used to look up the user
	// again on refresh (so that role changes take effect) and to populate
	// the StaffID claim in minted tokens.
	ID int64 `json:"id"`

	// Email is the login email as stored on the staff_users row. It is
	// copied into the access token's email claim, where it serves as an
	// audit-enrichment value only.
	Email string `json:"email"`

	// Password is the bcrypt hash from staff_users.password_hash, *not* the
	// plaintext supplied by the client. See the type-level doc for the
	// naming caveat and the comparison rule.
	Password string `json:"password"`
}
