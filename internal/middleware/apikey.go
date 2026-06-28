package middleware

import (
	"net/http"

	"hello/pkg/response"
)

type APIKeyMiddleware struct {
	apiKey string
}

func NewAPIKeyMiddleware(apiKey string) *APIKeyMiddleware {
	return &APIKeyMiddleware{
		apiKey: apiKey,
	}
}

func (a *APIKeyMiddleware) Use(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providedAPIKey := r.Header.Get("X-API-Key")

		if providedAPIKey == "" || providedAPIKey != a.apiKey {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid api key")
			return
		}

		next.ServeHTTP(w, r)
	})
}
