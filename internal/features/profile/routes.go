package profile

import (
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all profile endpoints with the provided ServeMux.
func (m *ProfileModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1"

	use := middleware.Chain(m.middlewares...)

	mux.HandleFunc("GET "+prefix+"/profile", use(m.handler.GetProfile))
	mux.HandleFunc("PATCH "+prefix+"/profile", use(m.handler.UpdateProfile))
	mux.HandleFunc("PATCH "+prefix+"/profile/password", use(m.handler.UpdatePassword))
}
