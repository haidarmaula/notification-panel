package main

import (
	"fmt"
	"hello/internal/auth"
	"hello/internal/middleware"
	"hello/internal/notifications"
	"net/http"
)

func main() {
	mux := http.NewServeMux()

	auth.RegisterModule(mux)
	notifications.RegisterModule(mux)

	fmt.Println("server running :8080")
	http.ListenAndServe(":8080", middleware.Logging(mux))
}
