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

// JWTMiddleware validates the Bearer access token on incoming requests and
// exposes the parsed claims to downstream handlers via the request context.
//
// It is the primary authentication layer for staff-facing endpoints. The
// API key middleware is expected to run before it, but the two are
// independent: JWT validation does not inspect the API key, and vice versa.
//
// The middleware is stateless after construction and safe for concurrent
// use. It does not perform token revocation or refresh; those are handled by
// the auth service at the endpoint level, not here.
type JWTMiddleware struct {
	tokenManager *token.TokenManager
}

// NewJWTMiddleware constructs a JWTMiddleware that delegates token parsing to
// the supplied tokenManager.
//
// tokenManager must be non-nil; it is stored directly and dereferenced on
// the first request, so a nil value will cause a panic at request time
// rather than at construction. The caller is expected to have built it once
// at process startup via token.NewTokenManager and to share the single
// instance across all middleware and handlers.
func NewJWTMiddleware(tokenManager *token.TokenManager) *JWTMiddleware {
	return &JWTMiddleware{
		tokenManager: tokenManager,
	}
}

// Use wraps next with Bearer token validation.
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
// responsible for signature verification, expiry checking, and any other
// claim validation the token package performs. This middleware does not
// inspect the claims beyond what that call returns; a token that parses
// successfully is considered authenticated, irrespective of the staff user's
// current status in the database. Callers that need to reject deactivated
// staff must do so at the handler or service layer.
//
// # Failure responses
//
// Every failure path returns a 401 JSON envelope. A missing Authorization
// header produces "missing token"; every other failure — malformed header,
// unsupported scheme, invalid or expired signature — produces "invalid
// token". No detail about the underlying parse error is surfaced to the
// client; the underlying error is not logged here either, so the caller
// should add logging at the handler layer if diagnosis is needed.
//
// # Context propagation
//
// On success the parsed *token.AccessClaims is attached to a copy of the
// request context under UserContextKey, and next is invoked with the
// augmented request via r.WithContext. The original request is not mutated.
//
// # Concurrency
//
// The returned handler is safe for concurrent use. The receiver holds an
// immutable pointer to the token manager, and no per-request state is
// shared between invocations.
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

		ctx := context.WithValue(r.Context(), UserContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
