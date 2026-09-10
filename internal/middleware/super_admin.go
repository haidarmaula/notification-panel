package middleware

import (
	"hello/internal/token"
	"hello/pkg/response"
	"net/http"
)

// SuperAdminMiddleware restricts a route to staff users whose role matches a
// single configured super-admin role name.
//
// It is an authorization middleware, not an authentication one: it assumes
// the request has already passed through JWTMiddleware, which places the
// parsed *token.AccessClaims on the request context under UserContextKey.
// SuperAdminMiddleware reads those claims and compares claims.RoleName
// against its configured value. It performs no token parsing, signature
// verification, or database lookup of its own.
//
// # Role comparison
//
// The comparison is a case-sensitive string equality against the configured
// role name, which is typically sourced from cfg.SuperAdminRole. There is no
// hierarchy and no set membership: a user whose role is "ADMIN" is rejected
// by an instance configured for "SUPER_ADMIN" even if ADMIN is conceptually
// more privileged than a lower role. Callers that need multi-role
// authorization must compose additional middleware or extend this one;
// doing so is a code change, not a configuration change.
//
// # Chain position
//
// The intended chain order is APIKey → JWT → SuperAdmin. Running it before
// JWT is not merely ineffective — it is unsafe, because the type assertion
// on UserContextKey will fail and the middleware will return 401 for every
// request, masking the fact that JWT is missing rather than surfacing it.
// Running it after JWT is what makes the 403 branch reachable.
//
// # Concurrency
//
// The middleware is stateless after construction apart from the configured
// role string, and is safe for concurrent use.
type SuperAdminMiddleware struct {
	superAdminRole string
}

// NewSuperAdminMiddleware constructs a SuperAdminMiddleware that will accept
// any request whose token claims carry exactly the supplied role name.
//
// superAdminRole is typically cfg.SuperAdminRole, which the config loader
// guarantees is non-empty. An empty superAdminRole is not rejected here, but
// because the comparison is string equality against a non-empty
// claims.RoleName, an empty configured value causes the middleware to reject
// every authenticated request with 403. That is fail-closed and preferable
// to the alternative (fail-open) but produces a confusing error for the
// operator; a startup-time validation of cfg.SuperAdminRole would surface
// the problem earlier.
func NewSuperAdminMiddleware(superAdminRole string) *SuperAdminMiddleware {
	return &SuperAdminMiddleware{
		superAdminRole: superAdminRole,
	}
}

// Use wraps next with the super-admin role check.
//
// # Flow
//
// The middleware reads the request context's UserContextKey and type-asserts
// the value to *token.AccessClaims. It then compares claims.RoleName against
// the configured role and either rejects the request or invokes next. Three
// distinct outcomes are possible:
//
//   - No claims on the context, or claims of the wrong type: the request is
//     rejected with 401 "authentication required". This path is expected to
//     be unreachable when the middleware runs after JWTMiddleware; if it
//     fires, the most likely cause is a misordered chain.
//   - Claims present but RoleName does not match: the request is rejected
//     with 403 "insufficient permissions".
//   - Claims present and RoleName matches: next is invoked, unchanged.
//
// # What is not checked
//
// The middleware does not inspect any claim other than RoleName. In
// particular, it does not verify that the staff user is still active in the
// database — a deactivated staff user with an unexpired token will still
// pass this check. Callers that need status-aware authorization must add a
// subsequent check at the handler or service layer.
//
// # Failure responses
//
// Both rejection paths return a JSON envelope through response.JSON. The
// 401-versus-403 distinction is deliberate: 401 signals "you are not
// authenticated", 403 signals "you are authenticated but not permitted".
// Clients should treat the two as different recovery paths.
//
// # Concurrency
//
// The returned handler is safe for concurrent use. The receiver holds only
// the configured role string, which is read-only after construction, and
// per-request state is confined to the context.
func (m *SuperAdminMiddleware) Use(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(UserContextKey).(*token.AccessClaims)
		if !ok {
			response.JSON(w, http.StatusUnauthorized, nil, "authentication required")
			return
		}

		if claims.RoleName != m.superAdminRole {
			response.JSON(w, http.StatusForbidden, nil, "insufficient permissions")
			return
		}

		next.ServeHTTP(w, r)
	})
}
