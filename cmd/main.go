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

	mux.HandleFunc("GET /notifications", h.GetAll)
	mux.HandleFunc("GET /notifications/{id}", h.GetByID)
	mux.HandleFunc("POST /notifications", h.Create)
	mux.HandleFunc("DELETE /notifications/{id}", h.Delete)

	fmt.Println("server running :8080")
	http.ListenAndServe(":8080", middleware.Logging(mux))
}
