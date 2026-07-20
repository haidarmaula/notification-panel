package auth

import (
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all authentication endpoints with the provided ServeMux.
func (m *AuthModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1/auth"

	use := middleware.Chain(m.middlewares...)

	mux.HandleFunc("POST "+prefix+"/login", use(m.handler.Login))
	mux.HandleFunc("POST "+prefix+"/refresh", use(m.handler.RefreshToken))
}
