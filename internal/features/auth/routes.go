package auth

import (
	"net/http"

	"hello/internal/middleware"
)

// RegisterRoutes applies the same server-to-server API-key guard to all staff credential exchanges.
func (m *AuthModule) RegisterRoutes(mux *http.ServeMux) {
	use := middleware.Chain(m.middlewares...)
	const prefix = "/api/v1/auth"
	mux.HandleFunc("POST "+prefix+"/login", use(m.handler.Login))
	mux.HandleFunc("POST "+prefix+"/refresh", use(m.handler.RefreshToken))
	mux.HandleFunc("POST "+prefix+"/logout", use(m.handler.Logout))
}
