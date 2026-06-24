package notifications

import (
	"net/http"
)

func RegisterModule(mux *http.ServeMux) {
	notificationRepo := NewNotificationRepository()
	notificationService := NewNotificationService(notificationRepo)
	notificationHandler := NewNotificationHandler(notificationService)

	RegisterRoutes(mux, notificationHandler)
}
