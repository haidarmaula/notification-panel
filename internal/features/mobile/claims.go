// Package mobile implements the mobile-app integration endpoint that
// synchronises device push tokens against users authenticated by an
// upstream Laravel backend.
//
// # Scope
//
// The package exposes a single endpoint, POST /api/v1/mobile/sync. Its
// purpose is to register or refresh a device's push token and to associate
// it with the correct user row, given a JWT that the mobile client obtained
// from Laravel during its own login flow. The endpoint does not
// authenticate the mobile user in the sense that the staff-facing auth
// package does; it verifies a token issued by a different system and treats
// the claims within that token as authoritative for user identity.
//
// # Trust boundary
//
// The JWT arrives in the JSON request body rather than in the Authorization
// header. This is deliberate: the endpoint is not session-authenticated —
// the mobile client is exchanging a Laravel-issued token for a device
// registration, not presenting a bearer token on an established session.
// The token is verified using MOBILE_JWT_SECRET, which is a distinct secret
// from the staff-facing ACCESS_SECRET and REFRESH_SECRET; the three secrets
// are configured independently, and a token issued for one audience is not
// valid for another.
//
// # Endpoint behaviour
//
// The endpoint is idempotent at the (user, push_token) level: repeated syncs
// from the same device update the existing device_tokens row rather than
// creating duplicates. This makes it safe for the mobile client to call on
// every app launch without tracking local registration state.
//
// # Layering
//
// The package follows the standard feature-module triad used elsewhere in
// this repository:
//
//   - claims.go declares the JWT claim shape expected from Laravel.
//   - dto.go declares the request and response JSON shapes for the endpoint.
//   - model.go declares the domain views of a mobile user, a mobile device,
//     and the sync result.
//   - service.go implements the sync business logic.
//   - handler.go adapts the HTTP request to a service call.
//   - routes.go (if present) registers the endpoint on the shared ServeMux.
//
// # Concurrency
//
// Every type in this package is safe for concurrent use after construction.
// The handler shares a single *MobileService, which holds no per-request
// state.
package mobile

import "github.com/golang-jwt/jwt/v5"

// MobileClaims represents the JWT claims from Laravel backend.
//
// The claim shape is defined by the upstream Laravel system and is not
// under this repository's control. The three custom claims —
// external_id, name, and email — are the subset that this service depends
// on; Laravel may emit additional claims, which the JWT library will
// ignore on decode.
//
// # Registered claims
//
// The embedded jwt.RegisteredClaims supplies the standard fields — exp,
// iat, iss, sub, and so on. Verification of exp is performed by the JWT
// library during parse; the mobile service does not re-check it. The
// service also does not enforce iss, aud, or any other registered claim
// beyond what the library validates by default, so a token minted by any
// system that shares MOBILE_JWT_SECRET and produces a compatible shape is
// accepted.
//
// # Identity model
//
// ExternalID is the authoritative user identifier for this flow. It is the
// value Laravel uses for the user, and it maps to the users.external_id
// column in this service's database. It is deliberately not the same as the
// internal users.id — see the users package for the identity model — so
// callers must not assume the two are interchangeable.
type MobileClaims struct {
	// ExternalID is the identifier Laravel assigns to the mobile user. It
	// is used to look up (or create) the corresponding row in this
	// service's users table.
	ExternalID string `json:"external_id"`

	// Name is the user's display name as known to Laravel. It is copied
	// into the local users row on first sync and refreshed on subsequent
	// syncs if it differs.
	Name string `json:"name"`

	// Email is the user's email address as known to Laravel. Like Name, it
	// is reflected into the local users row on sync. It is not used as an
	// identity key; see ExternalID for that purpose.
	Email string `json:"email"`

	jwt.RegisteredClaims
}
