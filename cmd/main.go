package main

import (
	"fmt"
	"hello/internal/middleware"
	"hello/internal/notifications"
	"net/http"
)

func main() {
	repo := notifications.NewNotificationRepository()
	svc := notifications.NewNotificationService(repo)
	h := notifications.NewNotificationHandler(svc)

	mux := http.NewServeMux()

	notifications.RegisterRoutes(mux, h)

	fmt.Println("server running :8080")
	http.ListenAndServe(":8080", middleware.Logging(mux))
}
