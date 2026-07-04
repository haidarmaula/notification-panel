package notifications

import (
	"hello/internal/middleware"
	"net/http"
)

func (m *NotificationModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1/notifications"

	use := middleware.Chain(m.middlewares...)

	mux.Handle("GET "+prefix, use(m.handler.GetAll))
	mux.Handle("GET "+prefix+"/{id}", use(m.handler.GetByID))
	mux.Handle("POST "+prefix, use(m.handler.Create))
	mux.Handle("DELETE "+prefix+"/{id}", use(m.handler.Delete))
}
