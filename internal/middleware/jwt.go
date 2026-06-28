package middleware

import (
	"context"
	"net/http"
	"strings"

	"hello/internal/token"
	"hello/pkg/response"
)

type contextKey string

const UserContextKey = contextKey("user")

type JWTMiddleware struct {
	tokenManager *token.TokenManager
}

func NewJWTMiddleware(tokenManager *token.TokenManager) *JWTMiddleware {
	return &JWTMiddleware{
		tokenManager: tokenManager,
	}
}

func (j *JWTMiddleware) Use(next http.HandlerFunc) http.HandlerFunc {
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
		claims, err := j.tokenManager.ParseAccessToken(tokenString)
		if err != nil {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid token")
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
