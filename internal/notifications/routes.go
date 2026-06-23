package notifications

import (
	"net/http"
)

func RegisterRoutes(mux *http.ServeMux, handler *NotificationHandler) {
	const prefix = "/api/v1/notifications"

	mux.HandleFunc("GET "+prefix, handler.GetAll)
	mux.HandleFunc("GET "+prefix+"/{id}", handler.GetByID)
	mux.HandleFunc("POST "+prefix, handler.Create)
	mux.HandleFunc("DELETE "+prefix+"/{id}", handler.Delete)
}
