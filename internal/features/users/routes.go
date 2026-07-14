package users

import (
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all user endpoints with the provided ServeMux.
func (m *UserModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1"
	use := middleware.Chain(m.middlewares...)

	// User endpoints (admin)
	mux.HandleFunc("GET "+prefix+"/users", use(m.handler.List))
	mux.HandleFunc("GET "+prefix+"/users/search", use(m.handler.Search))
	mux.HandleFunc("GET "+prefix+"/users/{id}", use(m.handler.GetByID))
	mux.HandleFunc("GET "+prefix+"/users/{id}/segments", use(m.handler.GetUserSegments))
	mux.HandleFunc("GET "+prefix+"/users/{id}/notifications", use(m.handler.GetUserNotifications))

	// User device tokens (admin view)
	mux.HandleFunc("GET "+prefix+"/users/{id}/device-tokens", use(m.deviceTokenHandler.ListByUser))

	// Device token management (mobile API)
	mux.HandleFunc("POST "+prefix+"/device-tokens", use(m.deviceTokenHandler.Register))
	mux.HandleFunc("PATCH "+prefix+"/device-tokens/{id}", use(m.deviceTokenHandler.Update))
	mux.HandleFunc("DELETE "+prefix+"/device-tokens/{id}", use(m.deviceTokenHandler.Delete))
}
