// Package profile implements the authenticated staff user's self-service
// endpoints for managing their own account.
//
// # Scope
//
// The package is the counterpart of the staff feature: where staff handles
// administrative CRUD over other staff accounts, profile handles a staff
// user's access to their own record. It exposes three endpoints — read the
// current user's profile, update their name or email, and change their
// password — and enforces that the acting user can only act on themselves,
// because every operation derives its target ID from the request's JWT
// claims rather than from a path parameter.
//
// # Relationship to auth
//
// The profile feature shares the staff_users table and the bcrypt password
// store with the auth feature. Password changes performed here take effect
// for future login attempts immediately; like any other staff credential
// change in this codebase, they do not invalidate outstanding tokens — see
// the token package for the no-revocation caveat. A client that has changed
// its password retains its current access and refresh tokens until they
// expire.
//
// # Layering
//
// The package follows the standard feature-module triad used elsewhere in
// this repository:
//
//   - context.go declares the helper that extracts the acting staff
//     user's identity from the request context.
//   - dto.go declares the wire shapes for the three endpoints.
//   - model.go declares the domain view of a staff profile.
//   - service.go implements the business logic, including the bcrypt
//     verification of the current password on change.
//   - handler.go adapts HTTP to service calls and maps errors to status
//     codes.
//   - module.go and routes.go wire the feature into the HTTP server.
//
// # Concurrency
//
// Every type in this package is safe for concurrent use after construction.
// The handler shares a single *ProfileService, which holds no per-request
// state.
package profile

import (
	"context"

	"hello/internal/middleware"
	"hello/internal/token"
)

// GetStaffIDFromContext retrieves the staff ID from the JWT claims stored in context.
//
// It reads middleware.UserContextKey and type-asserts the stored value to
// *token.AccessClaims, returning the StaffID field and a boolean indicating
// whether a valid claim was found. A missing key, a value of the wrong
// type, or a nil claims pointer all yield (0, false); handlers in this
// package treat that result as an unauthenticated request and return 401.
//
// # Relationship to other helpers in the codebase
//
// This function is semantically identical to audit.GetStaffID and to the
// unexported notifications.getStaffIDFromContext. All three read the same
// context key, perform the same type assertion, and return the same two
// values. The duplication is structural — the profile package cannot import
// the audit package without creating a cycle through middleware, and the
// notifications package has the same constraint — but it does mean a
// change to the context representation must be applied in three places.
//
// A future refactor that moves the extraction into a small dependency-free
// helper package would collapse the three into one; the comment here is
// documentation of the current state, not an endorsement of it.
//
// # Return-value semantics
//
// The returned ID is the internal staff_users.id, not any external
// identifier. A returned value of 0 with ok == true is theoretically
// possible (a token with a StaffID of 0) but is treated as unauthenticated
// by the package's handlers, which check both ok and staffID != 0.
func GetStaffIDFromContext(ctx context.Context) (int64, bool) {
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	if !ok || claims == nil {
		return 0, false
	}
	return claims.StaffID, true
}
