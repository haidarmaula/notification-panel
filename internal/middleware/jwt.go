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

// StaffSessionChecker resolves current database authority for a signed family.
type StaffSessionChecker interface {
	SessionActive(context.Context, int64, string) (bool, error)
}

type JWTMiddleware struct {
	tokenManager *token.TokenManager
	staffChecker StaffSessionChecker
}

func NewJWTMiddleware(manager *token.TokenManager, checker StaffSessionChecker) *JWTMiddleware {
	return &JWTMiddleware{tokenManager: manager, staffChecker: checker}
}

// Use verifies the access credential and current database session before
// passing claims to an endpoint. Infrastructure failures fail closed.
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

		active, err := m.staffChecker.SessionActive(r.Context(), claims.StaffID, claims.SessionID)
		if err != nil {
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
			return
		}
		if !active {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid token")
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
