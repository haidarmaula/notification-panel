// Package token issues and verifies the JWTs that authenticate staff users
// of the Notification Panel admin portal.
//
// # Scope
//
// The package deals exclusively with staff authentication. Mobile-app
// authentication is handled by a separate, upstream system (the Laravel
// backend) whose tokens are verified by the API server via a distinct
// shared secret; that verification path does not flow through this package.
//
// # Token model
//
// Two token classes are minted, each with its own HMAC secret and its own
// claim shape:
//
//   - Access tokens (AccessClaims), short-lived and sent on every API
//     request in the Authorization header. They carry the acting staff
//     user's ID, role name, and email.
//   - Refresh tokens (RefreshClaims), long-lived and exchanged at a
//     dedicated endpoint for a fresh access token. They carry only the ID
//     and role, on the principle that a token whose sole purpose is to
//     obtain another token should reveal as little as possible if it leaks.
//
// Both classes are signed with HS256 (HMAC-SHA-256) and carry a `type` claim
// that is checked on parse, so a refresh token presented as an access token
// (or vice versa) is rejected.
//
// # Secrets
//
// The two classes use distinct HMAC secrets — accessSecret and
// refreshSecret, supplied by config.ServerConfig — so that compromise of one
// class does not automatically grant the attacker the other. Secrets are
// held as raw []byte; no length validation is performed, and callers are
// expected to source them from a strong random generator (for example, the
// output of `openssl rand -hex 32`).
//
// # Revocation
//
// Tokens are stateless and are not tracked server-side. There is no
// mechanism in this package for revoking an issued token before its
// `exp` claim elapses: logout is a client-side operation, and a password
// change does not invalidate outstanding refresh tokens. Deployments that
// require revocation must add a token-version column to the staff user
// record and check it at parse time; that is a cross-cutting change and
// belongs to a future revision, not to this package alone.
//
// # Concurrency
//
// A TokenManager is read-only after construction and safe for concurrent
// use. All methods take value or pointer receivers on immutable state and
// perform no locking internally.
//
// # Robustness
//
// Both parse methods validate input in the order (1) parse error,
// (2) token validity, (3) claim shape. The parsed token and its Claims
// field are each guarded before dereference, so no input string — however
// malformed — can crash the caller.
package token

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AccessClaims is the claim set embedded in an access token.
//
// It carries enough information to authorise a single API request without a
// database round-trip: the acting staff user's ID and role, and their email
// for audit-log enrichment. The registered claims embed supplies `exp` and
// `iat`; this package does not set `iss`, `sub`, `aud`, or `jti`.
//
// # Wire format
//
// Field names do not all match their JSON tags. In particular, StaffID is
// serialised as "user_id", not "staff_id", to align with the claim shape
// used elsewhere in the system. Changing the tag is a breaking change for
// any consumer that decodes the token payload, including the admin portal
// if it ever inspects claims client-side.
//
// # Type discrimination
//
// Type is always the literal string "access" for tokens minted by this
// package. ParseAccessToken rejects any token whose Type is not "access",
// which is how the two token classes are kept separate at the parser. Do not
// change this value without updating the parser in lockstep.
type AccessClaims struct {
	// StaffID is the internal staff_users.id of the authenticated user. It
	// is serialised as "user_id".
	StaffID int64 `json:"user_id"`

	// RoleName is the staff user's role as of token issuance. Authorization
	// decisions in the middleware chain compare against this value, so a
	// role change does not take effect until the access token is refreshed.
	RoleName string `json:"role_name"`

	// Email is the staff user's login email as of token issuance. It is
	// carried purely for audit enrichment; authorization never depends on
	// it. A change of email does not invalidate outstanding tokens.
	Email string `json:"email"`

	// Type is the token-class discriminator. For access tokens minted by
	// GenerateAccessToken it is always "access".
	Type string `json:"type"`

	jwt.RegisteredClaims
}

// RefreshClaims is the claim set embedded in a refresh token.
//
// It is deliberately smaller than AccessClaims: a refresh token is only ever
// presented at a single endpoint to mint a new access token, and the email
// is re-read from the database at that point, so carrying it here would only
// widen the blast radius of a leaked refresh token.
//
// # Wire format
//
// As with AccessClaims, StaffID serialises as "user_id". The Type field is
// always the literal string "refresh".
type RefreshClaims struct {
	// StaffID is the internal staff_users.id of the user the refresh token
	// was issued to. It is serialised as "user_id".
	StaffID int64 `json:"user_id"`

	// RoleName records the role at issuance for compatibility. Refresh resolves
	// the current role through the database; this claim grants no authority.
	RoleName string `json:"role_name"`

	// Type is the token-class discriminator. For refresh tokens minted by
	// GenerateRefreshToken it is always "refresh".
	Type string `json:"type"`

	jwt.RegisteredClaims
}

// Time-to-live values for the two token classes.
//
// These are compile-time constants rather than configuration: adjusting the
// session lifetime is a code change, not a deployment change. That is a
// deliberate simplification for the current scale, but it means a
// deployment cannot, for example, shorten access-token lifetime in a
// staging environment without a separate build.
//
// The values reflect the intended security posture: short access lifetimes
// keep the window of misuse for a leaked token small, while a longer refresh
// lifetime keeps the login experience tolerable for staff.
const (
	// accessTTL is how long an access token remains valid after issuance.
	// The chosen value — 15 minutes — is short enough that a leaked token
	// is unlikely to be useful for long, and long enough that a working
	// session does not require constant refresh calls.
	accessTTL = 15 * time.Minute

	// refreshTTL is how long a refresh token remains valid after issuance.
	// At 24 hours, it roughly corresponds to a single working day, so a
	// staff member who logs in at the start of their shift is not forced to
	// re-authenticate before the end of it.
	refreshTTL = 24 * time.Hour
)

// ErrInvalidToken is the single sentinel error returned by every parse
// method for every form of token failure.
//
// Expired tokens, tokens with an invalid signature, tokens minted with the
// wrong secret, malformed token strings, and tokens of the wrong class
// (access presented to ParseRefreshToken, or vice versa) are all collapsed
// into this one value. That is deliberate: it prevents an attacker from
// distinguishing "your token expired" from "your signature is wrong" from
// "you sent a refresh token", which would otherwise let them narrow the
// search space for a forgery attempt.
//
// The trade-off is that callers cannot build retry logic on the error alone.
// A client that wants to treat expiry differently from invalid signature
// must inspect the underlying jwt library error, which this package does
// not currently expose. Callers should compare with errors.Is rather than
// comparing the error value directly, in case a future revision wraps the
// sentinel with additional context.
var ErrInvalidToken = errors.New("invalid token")

// TokenManager mints and verifies JWTs for the admin portal.
//
// A single TokenManager is normally constructed once at process startup and
// shared across every middleware and handler that needs to issue or verify a
// token. It holds the two HMAC secrets as raw byte slices; no copy is made,
// and the secrets are never logged or exposed by any method.
//
// TokenManager is immutable after construction and safe for concurrent use
// from any number of goroutines.
type TokenManager struct {
	accessSecret  []byte
	refreshSecret []byte
}

// NewTokenManager constructs a TokenManager with the supplied HMAC secrets.
//
// accessSecret and refreshSecret are the raw secret strings from
// config.ServerConfig (ACCESS_SECRET and REFRESH_SECRET). They are converted
// to []byte by this constructor and held for the lifetime of the manager;
// the caller's string copies are not retained.
//
// # Secret expectations
//
// Neither secret is validated for length or entropy. HMAC-SHA-256 will
// accept a secret of any length, including the empty string, but a short or
// predictable secret makes the signature brute-forceable. Production
// deployments should source each value from a cryptographically random
// generator — 32 bytes of entropy, hex- or base64-encoded, is the
// conventional minimum. The two secrets should also be independent: using
// the same value for both defeats the class separation this package
// implements.
//
// # Nil-value behaviour
//
// Passing two empty strings yields a manager that will happily mint tokens
// signed with the empty key and validate tokens signed with the same empty
// key. This is not detected here; it is the config loader's responsibility
// to reject missing secrets before construction.
func NewTokenManager(accessSecret, refreshSecret string) *TokenManager {
	return &TokenManager{
		accessSecret:  []byte(accessSecret),
		refreshSecret: []byte(refreshSecret),
	}
}

// GenerateAccessToken mints a signed access token for the given staff user.
//
// # Parameters
//
//   - StaffID: the internal staff_users.id of the user the token is issued
//     to. It is written into the "user_id" claim; the caller is expected to
//     have already authenticated the user and located their row.
//   - roleName: the user's role name as of issuance. It is written into the
//     "role_name" claim and later compared by SuperAdminMiddleware against
//     the configured super-admin role.
//   - email: the user's login email. It is written into the "email" claim
//     and used only for audit enrichment downstream.
//
// # Issued claims
//
// The token carries `exp` (issued-at + accessTTL) and `iat` (now). It does
// not carry `iss`, `sub`, `aud`, `nbf`, or `jti`. The `type` claim is
// hardcoded to "access".
//
// # Errors
//
// The only failure mode is a signing error from the underlying JWT library,
// which in practice occurs only if the secret is unusable. Callers may
// safely ignore the error in environments where the secret is known to be
// valid, but idiomatic Go practice is to check it anyway and propagate.
func (t *TokenManager) GenerateAccessToken(StaffID int64, roleName string, email string) (string, error) {
	claims := AccessClaims{
		StaffID:  StaffID,
		RoleName: roleName,
		Email:    email,
		Type:     "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(accessTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString(t.accessSecret)
}

// GenerateRefreshToken mints a signed refresh token for the given staff user.
//
// # Parameters
//
//   - StaffID: the internal staff_users.id of the user the token is issued
//     to. It is written into the "user_id" claim and later surfaced to the
//     refresh handler so it can look up the current user record.
//   - roleName: the user's role name as of issuance. Written into the
//     "role_name" claim; it is copied forward into the new access token when
//     the refresh token is redeemed.
//
// The email is intentionally not carried — see RefreshClaims for the
// rationale.
//
// # Issued claims
//
// The token carries `exp` (issued-at + refreshTTL) and `iat` (now). The
// `type` claim is hardcoded to "refresh".
//
// # Errors
//
// As with GenerateAccessToken, the only realistic failure mode is a signing
// error from the JWT library.
func (t *TokenManager) GenerateRefreshToken(StaffID int64, roleName string) (string, error) {
	claims := RefreshClaims{
		StaffID:  StaffID,
		RoleName: roleName,
		Type:     "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(refreshTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString(t.refreshSecret)
}

// ParseAccessToken verifies a token string against the access secret and
// returns its claims.
//
// # What is verified
//
// The token must be a well-formed JWT, be signed with a key derivable from
// the access secret, and carry a `type` claim equal to "access". Expiry is
// enforced via the required `exp` claim; HS256 and a positive user_id are
// required. Any failure returns ErrInvalidToken.
//
// # Policy and remaining limits
//
// Only HS256 is accepted, exp is required, and user_id must be positive.
// Role/email are informational; current-DB authority is checked separately.
// No iss/aud/sub/session/key-ring policy is introduced by this parser baseline.
//
// # Failure behaviour
//
// On any failure the returned claims are nil and the error is ErrInvalidToken.
// The underlying JWT library error is discarded, not wrapped; callers cannot
// distinguish expired from tampered from wrong-class via this function alone.
//
// Malformed input is handled safely: the parse error and token validity
// are checked before any claim is dereferenced, and the type assertion
// uses the two-value form with an explicit nil guard. No input string,
// however malformed, can crash the caller.
func (t *TokenManager) ParseAccessToken(tokenString string) (*AccessClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &AccessClaims{}, func(_ *jwt.Token) (any, error) {
		return t.accessSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, ErrInvalidToken
	}
	// Defensive: jwt/v5 can return a non-nil token with a nil Claims field
	// on some error paths. Guard before the type assertion.
	if token == nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*AccessClaims)
	if !ok || claims == nil || claims.Type != "access" || claims.StaffID <= 0 {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// ParseRefreshToken verifies a token string against the refresh secret and
// returns its claims.
//
// # What is verified
//
// The token must be a well-formed JWT, be signed with a key derivable from
// the refresh secret, and carry a `type` claim equal to "refresh". Expiry is
// enforced via the required `exp` claim; HS256 and a positive user_id are
// required. Any failure returns ErrInvalidToken.
//
// # Relationship to ParseAccessToken
//
// The two methods verify against different secrets, so a token minted as an
// access token will not pass verification here (and vice versa) even before
// the `type` check is applied. The `type` check is therefore redundant
// defence-in-depth: it catches the case where both secrets have been
// misconfigured to the same value.
//
// # Failure behaviour
//
// On any failure the returned claims are nil and the error is ErrInvalidToken.
// The underlying JWT library error is discarded, not wrapped, so the caller
// cannot distinguish expired from tampered from wrong-class.
//
// Malformed input is handled safely: the parse error and token validity
// are checked before any claim is dereferenced, and the type assertion
// uses the two-value form with an explicit nil guard. No input string,
// however malformed, can crash the caller.
func (t *TokenManager) ParseRefreshToken(tokenString string) (*RefreshClaims, error) {
	parsedToken, err := jwt.ParseWithClaims(
		tokenString,
		&RefreshClaims{},
		func(_ *jwt.Token) (any, error) {
			return t.refreshSecret, nil
		},
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, ErrInvalidToken
	}
	// Defensive: jwt/v5 can return a non-nil token with a nil Claims field
	// on some error paths. Guard before the type assertion.
	if parsedToken == nil || !parsedToken.Valid {
		return nil, ErrInvalidToken
	}
	claims, ok := parsedToken.Claims.(*RefreshClaims)
	if !ok || claims == nil || claims.Type != "refresh" || claims.StaffID <= 0 {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
