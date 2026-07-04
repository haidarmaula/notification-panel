package middleware

import (
	"hello/internal/token"
	"hello/pkg/response"
	"net/http"
)

var superAdminID = 1

func SuperAdminMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(UserContextKey).(*token.AccessClaims)
		if !ok {
			response.JSON(w, http.StatusUnauthorized, nil, "authentication required")
			return
		}

		if claims.RoleID != int64(superAdminID) {
			response.JSON(w, http.StatusForbidden, nil, "insufficient permissions")
			return
		}

		next.ServeHTTP(w, r)
	})
}
