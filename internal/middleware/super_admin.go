package middleware

import (
	"hello/internal/token"
	"hello/pkg/response"
	"net/http"
)

type SuperAdminMiddleware struct {
	superAdminRole string
}

func NewSuperAdminMiddleware(superAdminRole string) *SuperAdminMiddleware {
	return &SuperAdminMiddleware{
		superAdminRole: superAdminRole,
	}
}

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
