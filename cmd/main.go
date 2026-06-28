package main

import (
	"fmt"
	"net/http"

	"hello/internal/auth"
	"hello/internal/config"
	"hello/internal/middleware"
	"hello/internal/notifications"
	"hello/internal/token"
)

func main() {
	cfg := config.Load()
	tm := token.NewTokenManager(cfg.AccessSecret, cfg.RefreshSecret)

	apiKeyMW := middleware.NewAPIKeyMiddleware(cfg.APIKey)
	jwtMW := middleware.NewJWTMiddleware(tm)

	mux := http.NewServeMux()

	authModule := auth.NewAuthModule(tm, apiKeyMW.Use)
	authModule.RegisterRoutes(mux)

	notificationModule := notifications.NewNotificationModule(apiKeyMW.Use, jwtMW.Use)
	notificationModule.RegisterRoutes(mux)

	fmt.Println("server running :8080")
	http.ListenAndServe(":8080", middleware.Logging(mux))
}
