package middleware

import (
	"net/http"
	"os"

	"hello/pkg/response"
)

func APIKeyMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providedAPIKey := r.Header.Get("X-API-Key")
		apiKey := os.Getenv("API_KEY")

		if providedAPIKey == "" || providedAPIKey != apiKey {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid api key")
			return
		}

		next.ServeHTTP(w, r)
	})
}
