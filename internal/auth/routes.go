package auth

import (
	"hello/internal/middleware"
	"net/http"
)

func RegisterRoutes(mux *http.ServeMux, handler *AuthHandler) {
	const prefix = "/api/v1/auth"

	mux.HandleFunc("POST "+prefix+"/login", middleware.APIKeyMiddleware(handler.Login))
	mux.HandleFunc("POST "+prefix+"/refresh", middleware.APIKeyMiddleware(handler.RefreshToken))
}
