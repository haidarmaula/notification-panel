package middleware

import (
	"context"
	"net/http"
	"strings"

	"hello/internal/token"
	"hello/pkg/response"
)

// contextKey is the private type used for context keys owned by this package.
//
// It exists so that keys set by this middleware cannot collide with keys set
// by other packages. See auditContextKey in audit.go for the rationale; the
// two types are intentionally distinct and must not be merged.
type contextKey string

// UserContextKey is the context key under which the parsed access token
// claims are stored.
//
// The value is a *token.AccessClaims set by JWTMiddleware.Use and read by
// the audit package's GetStaffID helper and by any handler that needs the
// authenticated staff user's identity. It is exported because those
// consumers live in different packages; callers within this repository
// should read it through audit.GetStaffID or a similar helper rather than
// performing a direct type assertion, so that the representation can change
// in one place.
//
// The value is absent on requests that did not pass through JWTMiddleware —
// for example, endpoints that are only gated by the API key middleware (the
// login endpoint itself). Handlers that require an authenticated staff user
// should always check the second return value of a type assertion rather
// than assuming presence.
const UserContextKey = contextKey("staff")

// StaffStatusChecker is the minimal contract that JWTMiddleware requires to
// verify that the staff user named by an access token is still allowed to
// use the system.
//
// The interface is deliberately narrow: it exposes a single active-status
// lookup rather than the full staff-user repository. This keeps the
// middleware package decoupled from internal/database/sqlc and lets a test
// supply a stub implementation without constructing a repository or a
// database connection.
//
// # Contract
//
// IsActive must return (true, nil) for an existing, enabled account,
// (false, nil) for an existing but disabled account, and (false, nil) for
// a non-existent account (the row was deleted after the token was minted).
// Only genuine infrastructure failures — connection errors, query errors —
// should be surfaced as a non-nil error; those propagate to the caller as a
// 500 response. A non-nil error is never treated as "the user is inactive".
type StaffStatusChecker interface {
	IsActive(ctx context.Context, id int64) (bool, error)
}

// JWTMiddleware validates the Bearer access token on incoming requests,
// confirms that the staff user is still active, and exposes the parsed
// claims to downstream handlers via the request context.
//
// It is the primary authentication layer for staff-facing endpoints. The
// API key middleware is expected to run before it, but the two are
// independent: JWT validation does not inspect the API key, and vice versa.
//
// The middleware is stateless after construction and safe for concurrent
// use. It does not perform token revocation or refresh; those are handled by
// the auth service at the endpoint level, not here.
//
// # Active-status check
//
// Tokens minted by the auth service are stateless and remain valid until
// their `exp` claim elapses. To close the window during which a deactivated
// account can still call the API with an old token, the middleware performs
// a database lookup on every authenticated request and rejects the request
// when the account is no longer active. The lookup adds one indexed query
// per request; callers that need to avoid that cost should consider a
// token-version column or a short-lived cache, both of which are outside
// the scope of this middleware.
type JWTMiddleware struct {
	tokenManager *token.TokenManager
	staffChecker StaffStatusChecker
}

// NewJWTMiddleware constructs a JWTMiddleware that delegates token parsing to
// the supplied tokenManager and active-status lookups to the supplied
// staffChecker.
//
// tokenManager and staffChecker must both be non-nil. Neither is validated
// here; a nil value will cause a panic at request time rather than at
// construction, matching the convention used by other constructors in this
// package. The caller is expected to have built the token manager once at
// process startup via token.NewTokenManager and to have constructed a
// StaffStatusChecker (typically
// *repository.StaffUserRepository) from the shared sqlc.Queries.
//
// # Interface-typed staffChecker
//
// Because staffChecker is an interface, tests may pass a stub without
// touching the database. The production wiring passes the concrete
// repository value; see cmd/server/main.go.
func NewJWTMiddleware(tokenManager *token.TokenManager, staffChecker StaffStatusChecker) *JWTMiddleware {
	return &JWTMiddleware{
		tokenManager: tokenManager,
		staffChecker: staffChecker,
	}
}

// Use wraps next with Bearer token validation and an active-status check.
//
// # Header parsing
//
// The middleware reads the Authorization header and requires the exact form
// "<scheme> <token>", separated by a single space. The scheme must be the
// literal string "Bearer" — the check is case-sensitive, even though RFC
// 7235 declares auth schemes case-insensitive, so a client sending
// "bearer abc" will be rejected. The token is split on the first space via
// strings.Split(authHeader, " "), which returns a slice of exactly two
// elements for a well-formed header; any other count (a missing space, a
// padded value, an extra trailing space) is rejected with the same error as
// a malformed token, so callers cannot distinguish the two failure modes
// from the response alone.
//
// # Token verification
//
// The extracted string is passed to tokenManager.ParseAccessToken, which is
// responsible for signature verification, expiry checking, and the
// token-class check the token package performs.
//
// # Active-status verification
//
// After the token parses successfully, the middleware consults
// staffChecker.IsActive with the StaffID from the token's claims. The
// following outcomes are distinguished:
//
//   - The lookup returns (true, nil): the account is enabled and the
//     request proceeds to the next handler with the claims attached to
//     the request context.
//   - The lookup returns (false, nil): the account is disabled or has been
//     deleted since the token was minted. The middleware responds with 401
//     "account is not active" and does not call next. The status code is
//     401 rather than 403 because the token no longer represents a valid
//     session, and the client's correct recovery is to re-authenticate.
//   - The lookup returns a non-nil error: the check could not be performed
//     for an infrastructure reason. The middleware responds with 500
//     "internal server error" and does not call next. This is deliberately
//     distinct from the inactive case: a database outage should not be
//     reported to the client as if the token had been rejected, because a
//     client that re-authenticates in response would still fail.
//
// # Failure responses
//
// A missing Authorization header produces 401 "missing token"; every other
// header or token failure produces 401 "invalid token"; an inactive account
// produces 401 "account is not active"; and a failure of the active-status
// lookup produces 500 "internal server error". No detail about the
// underlying error is surfaced to the client, and the errors are not logged
// here — callers that need diagnosis should add logging at the handler
// layer.
//
// # Context propagation
//
// On success the parsed *token.AccessClaims is attached to a copy of the
// request context under UserContextKey, and next is invoked with the
// augmented request via r.WithContext. The original request is not mutated.
//
// # Concurrency
//
// The returned handler is safe for concurrent use. The receiver holds
// immutable pointers to the token manager and the staff checker, and no
// per-request state is shared between invocations.
func (m *JWTMiddleware) Use(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			response.JSON(w, http.StatusUnauthorized, nil, "missing token")
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid token")
			return
		}

		tokenString := parts[1]
		claims, err := m.tokenManager.ParseAccessToken(tokenString)
		if err != nil {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid token")
			return
		}

		active, err := m.staffChecker.IsActive(r.Context(), claims.StaffID)
		if err != nil {
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
			return
		}
		if !active {
			response.JSON(w, http.StatusUnauthorized, nil, "account is not active")
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
