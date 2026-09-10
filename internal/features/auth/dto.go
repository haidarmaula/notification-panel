// Package auth implements the staff authentication endpoints of the
// Notification Panel: the login endpoint that exchanges credentials for a
// token pair, and the refresh endpoint that exchanges a refresh token for a
// new access token.
//
// # Scope
//
// The package covers staff authentication only. Mobile-app authentication is
// handled by an upstream system (the Laravel backend) and is not part of
// this package. Every authenticated principal produced here is a staff
// user, and the tokens minted here are the ones the middleware chain
// (JWTMiddleware, SuperAdminMiddleware) subsequently verifies.
//
// # Layering
//
// The package follows the standard feature-module triad used elsewhere in
// this repository:
//
//   - dto.go declares the request and response shapes bound to the JSON
//     bodies of the two endpoints.
//   - model.go declares the minimal domain view of a staff user that the
//     service layer needs in order to verify a password and mint tokens.
//   - service.go implements the login and refresh business logic, delegating
//     token issuance to internal/token and persistence to the staff-users
//     repository.
//   - handler.go adapts HTTP requests to service calls and maps domain
//     errors to status codes.
//   - routes.go registers the two endpoints on the shared ServeMux.
//
// # What this package deliberately does not do
//
// It does not implement logout, password reset, account lockout, brute-force
// throttling, or multi-factor authentication. Logout is a client-side
// operation today because tokens are stateless (see internal/token for the
// no-revocation caveat). The remaining features are listed as gaps in the
// project README and are expected to be added as separate concerns rather
// than folded into this package.
//
// # Concurrency
//
// Every type in this package is safe for concurrent use after construction.
// Handlers share a single *AuthService and a single *AuthHandler, neither of
// which holds per-request state.
package auth

// LoginRequest represents the payload for staff login endpoint.
//
// It is the JSON body expected by POST /api/v1/auth/login. Both fields are
// required by the service layer, though neither is validated here — an
// empty Email or Password is passed through and rejected by the credential
// check as an invalid-credentials error.
//
// The struct carries a plaintext Password field by necessity: this is the
// only place in the codebase where a plaintext password exists as a Go
// value after having crossed the network. It must not be logged, must not
// be persisted, and must not be reused outside the login call.
type LoginRequest struct {
	// Email is the staff user's login email. Matching is by exact string;
	// the service does not currently normalise case or trim whitespace.
	Email string `json:"email"`

	// Password is the plaintext password supplied by the client. The
	// service compares it against the stored bcrypt hash using
	// bcrypt.CompareHashAndPassword; it is never persisted.
	Password string `json:"password"`
}

// LoginResponse contains the tokens returned after successful authentication.
//
// Both tokens are opaque to the client: the access token is a JWT that the
// client sends back in the Authorization header on every subsequent request,
// and the refresh token is a JWT that the client redeems at the refresh
// endpoint. Neither is meant to be parsed by the client, and both should be
// stored in a location appropriate to the token's lifetime — the access
// token in memory, the refresh token in a secure store if the client
// supports one.
type LoginResponse struct {
	// AccessToken is a short-lived (15 minute) bearer token. See
	// internal/token.AccessClaims for the claims it carries.
	AccessToken string `json:"access_token"`

	// RefreshToken is a long-lived (24 hour) token that can only be
	// redeemed at the refresh endpoint. It carries fewer claims than the
	// access token by design.
	RefreshToken string `json:"refresh_token"`
}

// RefreshTokenRequest carries the refresh token to obtain a new access token.
//
// It is the JSON body expected by POST /api/v1/auth/refresh. The field is
// required; an empty value is passed to the service and rejected there as an
// invalid token rather than being caught as a malformed request.
type RefreshTokenRequest struct {
	// RefreshToken is the JWT previously issued by Login. It must carry a
	// "type" claim of "refresh"; an access token presented here is
	// rejected by the service.
	RefreshToken string `json:"refresh_token"`
}

// RefreshTokenResponse contains the new access token.
//
// Only the access token is returned: the refresh token is not rotated. A
// client that wants the security property of rotating refresh tokens would
// need the service to issue a new refresh token on every redemption and the
// response shape to carry it; that is not the current design.
type RefreshTokenResponse struct {
	// AccessToken is a freshly minted short-lived bearer token reflecting
	// the staff user's current role as read from the database at refresh
	// time.
	AccessToken string `json:"access_token"`
}
