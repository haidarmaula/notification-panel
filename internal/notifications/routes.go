package notifications

import (
	"hello/internal/auth"
	"net/http"
)

func protected(h http.HandlerFunc) http.HandlerFunc {
	return auth.JWTMiddleware(h)
}

func RegisterRoutes(mux *http.ServeMux, handler *NotificationHandler) {
	const prefix = "/api/v1/notifications"

	mux.Handle("GET "+prefix, protected(handler.GetAll))
	mux.Handle("GET "+prefix+"/{id}", protected(handler.GetByID))
	mux.Handle("POST "+prefix, protected(handler.Create))
	mux.Handle("DELETE "+prefix+"/{id}", protected(handler.Delete))
}
