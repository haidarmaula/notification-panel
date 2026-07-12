package notifications

import (
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all notification endpoints with the provided ServeMux.
func (m *NotificationModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1/notifications"
	use := middleware.Chain(m.middlewares...)

	mux.HandleFunc("GET "+prefix, use(m.handler.List))
	mux.HandleFunc("GET "+prefix+"/{id}", use(m.handler.GetByID))
	mux.HandleFunc("POST "+prefix, use(m.handler.Create))
	mux.HandleFunc("PATCH "+prefix+"/{id}", use(m.handler.Update))
	mux.HandleFunc("DELETE "+prefix+"/{id}", use(m.handler.Delete))
	mux.HandleFunc("POST "+prefix+"/{id}/send", use(m.handler.Send))
}
