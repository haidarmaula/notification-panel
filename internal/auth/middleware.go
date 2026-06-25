package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"hello/pkg/response"
)

type contextKey string

const UserContextKey = contextKey("user")

func JWTMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			response.JSON(w, http.StatusUnauthorized, nil, "missing token")
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid token format")
			return
		}
		tokenString := parts[1]

		token, err := jwt.ParseWithClaims(tokenString, &AccessClaims{}, func(t *jwt.Token) (any, error) {
			return accessSecret, nil
		})

		if err != nil || !token.Valid {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid token")
			return
		}

		claims, ok := token.Claims.(*AccessClaims)
		if !ok {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid claims")
			return
		}

		if claims.Type != "access" {
			response.JSON(w, http.StatusUnauthorized, nil, "wrong token type")
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
